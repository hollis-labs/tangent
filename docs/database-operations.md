# Database operations: ownership, backup, restore, repair, and deletion

Implements [ADR 0002](./adr/0002-retention-and-draft-custody.md) §6 and §7.

Everything described here has been run against a populated fixture database.
Where a claim is about something that was **not** exercised, it says so.

---

## 1. The single-writer contract

Tangent opens SQLite with `journal_mode = WAL` and `SetMaxOpenConns(1)`. That
makes one *process* a single writer and says nothing about two. It matters more
than it usually would, because the retention operations below drop immutability
triggers for the length of one transaction; a second writer during that window
would write into a database with no guards.

**Enforcement.** Every mode that writes takes a non-blocking exclusive
`flock(2)` on `<database>.owner` before the database is opened, and refuses
rather than queues when another process holds it:

```
$ tangent
tangent: another process holds the Tangent database: pid 58448, role server,
         on Chrispians-MacBook-Pro.local since 2026-09-05T00:06:27Z
```

The sidecar's JSON contents (pid, role, host, command, start time) are advisory
metadata that the refusal quotes. **The kernel lock is the authority; the file
is the explanation.** A killed process leaves the file behind and the kernel
drops the lock, so the next acquirer succeeds without anyone deleting anything.

**A second check, because the lock alone has a hole.** A Tangent deployed
before this landed takes no lock. So every command that needs exclusive
ownership *also* probes `http://127.0.0.1:<port>/readyz` first, and refuses if
anything answers:

```
$ tangent --db-repair
tangent: a Tangent is serving on port 7842 (/readyz answered 200); stop it
         before running a maintenance command. If that port belongs to a
         different installation, set TANGENT_HTTP_PORT to the one this command
         should check
```

**What it does not claim.** The lock is advisory: a process that never calls
for it — `sqlite3` at a shell, say — can still open the database. It stops two
Tangents, not everything. It is also unreliable over a network filesystem,
which is not a configuration a single-user local host has. Platforms without
`flock` get a refusal, not a permissive stub.

**Which commands take it.** Readers do not, on purpose — diagnosing a live
installation must not require stopping it.

| Command | Exclusive ownership | Runs while serving |
|---|---|---|
| `--db-check`, `--retention-plan`, `--retention-history` | no | yes |
| `--db-backup` (online) | no | **yes** |
| `--db-backup --db-drain` | yes | no |
| `--db-restore`, `--db-compact`, `--db-repair` | yes | no |
| every retention operation | yes | no |
| serving (`tangent` with no command) | yes | — |

---

## 2. Backup

Backups are **opt-in**. Nothing runs unless you name a destination.

```bash
tangent --db-backup ~/backups/tangent-2026-09-05.db              # online
tangent --db-backup ~/backups/tangent-2026-09-05.db --db-drain   # drained
```

Both use `VACUUM INTO`. **A `cp` of `tangent.db` is not a backup** — the file
alone, without its `-wal` companion, is a silently stale database.

Each backup writes a sidecar `<path>.manifest.json` carrying an id, mode,
timestamp, schema version, size, SHA-256, integrity result, and the
preservation fingerprint of §4. The manifest deliberately holds the *base name*
of the source database and no path: it travels with the backup, and there is no
reason for it to carry the layout of the machine it came from.

### Online — "consistent while the service runs"

**Guarantees.** A complete, self-contained, integrity-checked database holding
every transaction committed before the copy's read transaction began.
`VACUUM INTO` runs inside a read transaction, so it sees one snapshot and never
a torn one, and WAL readers do not block writers, so the running service is not
stalled.

**Does not guarantee.** That a write committed *during* the copy is in it. The
point in time is the start of the copy, not the end.

*Verified:* a backup taken while 200 concurrent inserts ran on the same handle
passes `integrity_check`, `foreign_key_check`, and the guard inventory.

### Drained — "drains explicitly"

Everything the online mode guarantees, **plus**: nothing else held the
database, no write was in flight, and the WAL was checkpointed to zero length
before the copy — so the source is left in a state a byte copy could also have
captured. `PRAGMA wal_checkpoint(TRUNCATE)` is the drain: if it cannot
complete, something else is using the database, which is what you needed to
know.

**Use drained** before an upgrade that migrates, before a purge, and any time
the backup is the thing being relied on rather than a convenience. It is the
only mode that can say "nothing was mid-write".

### What a backup cannot do

**Redaction cannot reach a backup.** A backup taken before an erasure still
holds the erased content. This is asserted as a test, not assumed. Pass
`--backup-dir <dir>` to a retention operation and it surveys that directory's
manifests and records, in its audit row, which backups still hold what it
removed. With no `--backup-dir` the row reads `not-surveyed`, which is a weaker
and more honest statement than an empty list.

---

## 3. Restore

```bash
tangent --db-restore ~/backups/tangent-2026-09-05.db --confirm
```

Order of operations, which is the whole design:

1. **Verify the source first**, before touching the target: `integrity_check`,
   `foreign_key_check`, schema version not dirty and not ahead of this binary,
   immutability guards intact. A restore that overwrites a working database
   with a corrupt backup is worse than one that refuses.
2. **Close the handle**, then move the existing database and its `-wal`/`-shm`
   companions to `<database>.superseded-<timestamp>`. It is never deleted.
3. Copy the backup into place, reopen, and take a fresh fingerprint.
4. Compare the fingerprint property by property. Any difference is a failure.
5. On any failure after step 2, remove the partial copy and rename the
   superseded database back.

Restoring is a rollback in time, and that includes the erasure log: a restore
from a backup taken before an erasure brings the erased content back and
removes the `retention_operations` row that recorded its removal.

---

## 4. What a restore preserves

Five properties, each measured independently — a row count plus a SHA-256 over
sorted canonical tuples — so a failure names which one broke rather than
reporting that "something differs". Payload columns are excluded on purpose:
the fingerprint has to keep matching across a redaction that preserves
identity.

| Property | What it covers |
|---|---|
| `definition_digests` | every interaction's pinned binding: publisher, kind, version, revision, digest, source, schema identity and digest, assurance |
| `interaction_identities` | id, surface, caller scope, **idempotency key**, sequence, lifecycle state, revision, terminal cause, participant ref |
| `resolutions` | id, interaction, participant binding and authority, response kind, **integrity digest**, expected and presented revisions |
| `audit_history` | every `surface_events`, `interaction_events`, and `delivery_events` row by id, type, actor, authority, and revision transition |
| `delivery_obligations` | every `resolution_deliveries` and `terminal_notifications` row by id, idempotency key, lifecycle state, and revision |

Plus counts of pending obligations and of leases still held by the process that
was running when the backup was taken.

**Delivery obligations are reconciled, not dropped.** The restore reports what
it found; the reconciliation is `RecoverAfterRestart`, which runs at the next
boot. Doing it inside restore would mean two implementations of recovery.
*Verified end to end:* three interrupted obligations (a retryable resolution
delivery, a manual-reconciliation delivery, and a terminal notification) survive
a backup and restore, are reconciled by restart recovery on the restored file
with the retryable/manual distinction intact, leave the three identity
properties unchanged, and reconcile exactly once across two boots.

---

## 5. Integrity, repair, and compaction

```bash
tangent --db-check      # read-only; safe against a serving installation
tangent --db-repair     # exclusive
tangent --db-compact    # exclusive
```

`--db-check` reports `integrity_check`, `foreign_key_check`, migration status,
the immutability-guard inventory against the schema this binary embeds, the
storage footprint (page size and count, freelist, WAL bytes, journal mode,
foreign-key enforcement), who holds the lock, and the recent erasure log.

**`--db-repair` fixes exactly two things**, because a tool that claims to
repair a database and cannot is worse than one that says "restore from a
backup":

- **A missing immutability guard.** Recoverable because the reference schema is
  embedded: the expected inventory is produced by applying this binary's own
  migrations to a throwaway in-memory database, and a missing trigger is
  recreated from that reference's exact text. This is the failure mode ADR 0002
  §6 names as the specific danger of having a maintenance path at all.
- **An unbounded WAL**, by checkpointing.

Everything else — page corruption, a dirty schema, foreign-key violations — is
reported with the action that fixes it, and the action is a restore.

`--db-compact` runs `VACUUM` between two truncating checkpoints. It preserves
every row and every trigger; the fingerprint is asserted unchanged across it.

---

## 6. The immutability guards, and how they are suspended

Derived from the schema at migration 0012, not from the count ADR 0002 recorded
at `0a45caa`: **41 triggers, 12 of them `BEFORE DELETE`.** The ADR counted nine;
migrations 0006, 0007, and 0012 added
`terminal_outcome_acknowledgements_immutable_delete`,
`definition_manifests_immutable_delete`, and
`retention_operations_immutable_delete`. The test that asserts these numbers
fails when a migration adds an immutable table without telling the retention
path about it.

`DELETE` is aborted on: `definition_bindings`, `definition_manifests`,
`delivery_attempts`, `delivery_events`, `draft_revisions`, `interaction_events`,
`resolutions`, `retention_operations`, `surface_events`,
`surface_open_requests`, `terminal_outcome_acknowledgements`,
`terminal_outcome_retrievals`. SQLite fires these for foreign-key cascade
actions too, which is why deleting a surface used to fail on `surface_events`.

**The maintenance path** (`internal/db`, and nowhere else) drops only the
guards a given operation needs, inside one transaction:

1. Read each named trigger's SQL from `sqlite_master` — never a paraphrase in
   Go, because a guard recreated from a paraphrase no longer says what the
   migration said.
2. `DROP TRIGGER` each.
3. Run the operation.
4. Recreate each from the saved text, then **verify each is back** by querying
   the catalog, still inside the transaction. A guard that is not back refuses
   the commit.
5. Commit.

Three layers contain the window in which a guard is absent. SQLite DDL is
transactional, so a rollback restores every dropped trigger — *verified by
injecting a failure and asserting both the inventory and that the guards still
abort*. `SetMaxOpenConns(1)` means the transaction holds this process's only
connection, so no other statement here can observe the window. The
single-writer lock means no other process can either.

Every operation records `guards_restored` in its audit row. A zero on a kind
that needed the path is the signature of the failure mode the ADR warns about.

**Migration 0012 also moved `surface_open_requests.surface_id` from
`ON DELETE RESTRICT` to `ON DELETE CASCADE`** (ADR 0002 §Q9, decided in this
work rather than deferred). The trigger stays: the table still refuses `UPDATE`
and still refuses direct `DELETE`. What changed is that a cascade from a surface
the maintenance path is erasing now reaches the second permanent copy of the
caller payload it was holding.

---

## 7. The six deletion kinds

They are separate verbs with separate authority and separate consequences. An
operator who cannot tell them apart will reach for the wrong one.

| Kind | Command | Authority | What it removes | Audit consequence |
|---|---|---|---|---|
| **capability expiry** | `--expire-capabilities` | host policy or local user | `effect_handles` rows — live grants, never content | `capability-expiry` row, `guards_restored: false` because no guard was suspended: `effect_handles` was designed deletable in migration 0009 |
| **draft deletion** | `--erase-drafts <interaction>` | the participant, or the local user | `draft_revisions.payload`, replaced by a tombstone; the row survives because the resolution names it as its source | `draft-deletion` row, plus the first rows this schema has ever put in `draft_revision_tombstones` |
| **payload redaction** | `--erase-interaction <id>`, `--erase-surface <id>`, `--retention-apply` | local user for an erasure; host policy for a window | caller payload, participant drafts, participant resolution, and adapter freeform text — replaced by the typed tombstone of §2 below | `payload-redaction` row carrying one digest per removed column |
| **surface close** | `--close-surface <id>` | local user or administrator | **nothing** | `surface-close` row with `affected_rows: 0` and a note saying every payload is still present — recorded precisely so a close cannot be mistaken for an erasure |
| **surface purge** | `--purge-surface <id>` | local user only, never automatic, never a window | the surface and everything that cascades: interactions, events, bindings, drafts, resolutions, deliveries, notifications, attempts, retrievals, acknowledgements, the open request, the legacy room and its envelopes, and the interactions' effect handles | `surface-purge` row that **outlives its own subject** — `retention_operations` has no foreign keys for exactly this reason |
| **external-source deletion** | `--external-deletion-report <interaction>` | none — Tangent has none to exercise | **nothing, and it cannot** | `external-source-deletion` row with outcome `refused` and code `external_source_unreachable`, listing the authorities and artifact ids to go delete at |

A redacted column becomes exactly ADR 0002 §2's shape:

```json
{"redacted": true, "policy_ref": "...", "at": "2026-09-05T00:06:17Z",
 "content_digest": "<sha-256 of the removed bytes>", "actor_ref": "operator:cli"}
```

The digest is the point: "was this the payload that produced that resolution?"
stays answerable after the content is gone. Redaction is idempotent — a second
pass finds tombstones, removes nothing, and never digests a digest.

What redaction preserves, by construction: identity, lifecycle, revisions,
timestamps, idempotency keys, `resolutions.integrity_digest`,
`resolutions.participant_ref`, `interactions.policy`,
`interactions.external_refs`, `definition_bindings.*`, and every
`*_events.metadata`. *Verified:* the five preservation properties of §4 are
unchanged across a redaction.

**Every destructive command requires `--confirm`.** `--dry-run` reports what
would be removed and writes nothing at all — not the change, and not an audit
row. A plan is not an act, and logging it as one would make the erasure log lie.
`--close-surface --dry-run` reports how many outstanding interactions the close
would cancel and does not close.

**Only operator-initiated closes appear in the erasure log.** A close performed
through `tangent.session_close` or the HTTP surface is recorded in
`surface_events`, which is where lifecycle audit belongs; putting every one of
them in `retention_operations` would bury the erasures under transitions that
removed nothing.

### Windows

`--retention-plan` lists what is past its window; `--retention-apply --confirm`
redacts it. Defaults are ADR 0002 §4's: 30 days after an interaction reaches a
terminal state, 90 days after a surface closes or expires, 14 days for a browser
draft, nothing for ephemeral. A window sweep **only ever redacts** — it never
deletes a row, because deleting one takes the idempotency key with it. It is
idempotent and restart-safe: each candidate is its own transaction, a partial
sweep leaves the rest eligible, and a second run finds tombstones.

**A non-terminal interaction is never eligible.** Eligibility is measured from
`terminal_at`, and a row without one is not in the answer — so a sweep cannot
race a participant who is still answering.

### The erasure log

`retention_operations` is append-only *and* undeletable — it refuses `DELETE`
as well as `UPDATE`, unlike `telemetry_events`, which permits `DELETE` because
its rows are unbounded observations rather than the record of an erasure. It
carries no content by construction: digests, identifiers, counts, and states.
It has no foreign keys, so a purge cannot cascade away the row that records it.
Read it with `tangent --retention-history` or over MCP.

`refused` and `failed` are first-class outcomes. An operation that correctly
declined — the target does not exist, the content lives outside Tangent — is a
fact worth keeping.

---

## 8. MCP surface

One tool, read-only: **`tangent.retention_status`** (live `tools/list` = 46).

It reports whether the guards are intact, the schema and storage state, whether
a process holds the lock and in what role, the windows in force, how many
interactions and surfaces are past them, the recent erasure log, and the
standing limitations of §9. It carries no payload, participant text,
filesystem path, hostname, or process identity — `internal/db.Status` is
*shaped* to ADR 0002 §8's floor rather than filtered down to it, and a test
asserts the database path never appears in a response.

**The six operations are deliberately not tools.** ADR 0002 §4 puts erasure
authority with the local user. Every one of these commands either removes a
participant's answer or destroys a record skeleton, and MCP is reachable by any
agent that can reach this host over a transport with no authenticated caller
identity — which ADR 0001 and ADR 0004 both defer. Exposing them would hand
deletion authority to whatever is on the other end of the socket.

---

## 9. What deletion cannot reach

Stated plainly rather than implied away, and returned with every
`tangent.retention_status`:

- **Backups.** A backup taken before an erasure still holds the content. The
  operation records which backups it knew about; it does not claim to have
  cleaned them.
- **Per-caller-scope deletion is unavailable for `standalone-local`.** Every
  local caller shares that scope, and after ADR 0004 any local caller can assert
  any partition, so a partition filter is a convenience for the local user and
  never a guarantee that one application's content has been isolated from
  another's. Fixing it needs the authenticated caller identity ADR 0001 and
  ADR 0004 both defer. This is documented, not engineered around.
- **External sources.** Tangent stores an authority, an artifact id, a
  revision, and a digest. The bytes were never here.
- **Browser drafts.** Nine `localStorage` families hold participant drafts
  outside the server's deletion boundary. The client-side gate and sweep of ADR
  0002 §5 are not implemented by this work.
- **Delivered payloads, exported artifacts, and anything a participant copied
  elsewhere.**
- **`surfaces.metadata`** is not redacted by `--erase-surface`. §10 of the ADR
  classifies it as presentation state; removing it from a surface still being
  rendered breaks the projection restore without removing anything a
  participant said. `--purge-surface` removes it with the row. Erasing it in
  place is a separate decision this work does not make silently.
- **`effect_receipts`** survive a purge. They are audit carrying a byte count
  and a SHA-256 — never content — and they are the record that a host-mediated
  effect happened at all. `effect_handles`, which are live grants, do not
  survive.
- **`telemetry_events`** are not removed by any retention operation. They carry
  no content by construction and are bounded by their own 30-day / 200k sweep.
- **`effect.SQLStore.RevokeHandlesForInteraction` is still without a caller.**
  `internal/db` sits below `internal/effect` and must not import it, and that
  method's own documentation names its call site as a terminal transition —
  which a retention operation is not. `--expire-capabilities` and
  `--purge-surface` delete the same rows directly instead.
- **The `/readyz` probe checks the port this binary would bind.** An operator
  whose `TANGENT_HTTP_PORT` does not match the running instance is not caught
  by that half; the ownership lock still catches it, and the refusal names the
  variable.
- **Windows** gets a refusal, not a lock. `internal/db` cross-compiles for it;
  nothing here has ever been run there.

## 10. Not implemented by this work

- **The custody precedence engine** of ADR 0002 §3. Nothing writes a `custody`
  object into `interactions.policy`, so eligibility is computed from the host
  window alone and a caller or definition that asked for longer retention is not
  yet honored. `--retention-plan` says this in its own output rather than
  leaving it to be discovered.
- **The `custody_assurance: "legacy-unknown"` backfill** of §12.
- **The browser draft gate, key normalization, and one-time sweep** of §5.
- **The `external-reference` submission refusal** of §9.1.
