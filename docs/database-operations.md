# Database operations: ownership, backup, restore, repair, and deletion

Implements [ADR 0002](./adr/0002-retention-and-draft-custody.md) §6 and §7.

Everything described here has been run against a populated fixture database,
and every claim about backup, restore, and verification has been re-run against
a source **two migrations older than the binary** — which is the only shape a
pre-upgrade backup ever has, and the shape CW-20260825-0072 never constructed.
Where a claim is about something that was **not** exercised, it says so.

**Older is not damaged.** The two questions this document keeps apart
everywhere below are *is this database broken* and *is this database the one
this binary was built for*. A backup taken before an upgrade is behind by
design; treating that as an integrity failure made the procedure in §2 and §3
impossible to follow (CW-20260905-0014). Every report here answers them in
separate fields: `integrity_ok` and `damage` for the first, `schema_state` and
`schema_advice` for the second.

---

## 1. The single-writer contract

Tangent opens SQLite with `journal_mode = WAL` and `SetMaxOpenConns(1)`. That
makes one *process* a single writer and says nothing about two. It matters more
than it usually would, because the retention operations below drop immutability
triggers for the length of one transaction; a second writer during that window
would write into a database with no guards.

**Enforcement, keyed on the database.** Every mode that writes takes a
non-blocking exclusive `flock(2)` on `<database>.owner` before the database is
opened, and refuses rather than queues when another process holds it. The
refusal names the process holding *that database* — pid, role, host, since
when:

```
$ tangent --db-repair
tangent: another process holds the Tangent database: pid 17783, role server,
         on Chrispians-MacBook-Pro.local since 2026-09-05T12:26:07Z; stop it,
         or wait for it to finish
```

The sidecar's JSON contents (pid, role, host, command, start time) are advisory
metadata that the refusal quotes. **The kernel lock is the authority; the file
is the explanation.** A killed process leaves the file behind and the kernel
drops the lock, so the next acquirer succeeds without anyone deleting anything.

**A readiness probe, as a note rather than a refusal.** A Tangent deployed
before the lock landed takes none, so a command that needs exclusive ownership
also probes `http://127.0.0.1:<port>/readyz` and says what it found:

```
tangent: note: something answers /readyz on port 7842 (/readyz answered 200).
         This command holds the single-writer lock on <database>, so that
         process is not writing the database this command will touch — unless
         it is a Tangent old enough to take no lock, in which case stop it
         first.
```

It used to refuse on that probe, and refusing was wrong: **a port is not a
database.** Nothing in an HTTP readiness answer says which database the
answering process has open, so the check was refusing maintenance on database A
because something was serving database B — which is exactly what happened when
a restore into a temporary database was blocked by the live installation on
7842, with `TANGENT_HTTP_PORT` as the only way through (CW-20260905-0014).
*Verified:* with the live instance serving on 7842, `--db-restore` into an
unrelated temporary database now succeeds, and a maintenance command against
the database that instance actually holds is still refused by the lock.

**The residual gap, stated plainly.** A Tangent from a build old enough to take
no lock is no longer *refused*; it is warned about. Every Tangent since
CW-20260825-0072 claims `<database>.owner` before it opens the database,
serving processes included, so the lock covers every build that exists. If a
pre-lock binary is somehow still running, the note above is what you get, and
stopping it is on you.

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

"Runs while serving" means *while a Tangent holds this database*. A Tangent
serving a **different** database does not block any of these, whatever port it
is on — that is the fix in the paragraphs above, and it is what makes working
on a copy possible without misstating `TANGENT_HTTP_PORT`.

### The multi-host escape hatch

SQLite is a strong local-first store **while one authoritative Tangent runtime
owns migrations, claims, and writes** — which is what the lock above enforces.
WAL, connection limits, transaction discipline, backup, and repair are part of
that deployment contract, not incidental tuning.

A shared or multi-host deployment can use another transactional store **without
changing the interaction model**. Horizontal processes coordinate through
durable claims and event delivery, not through process-local room maps. Nothing
in this document's operational contract — the six deletion kinds, the
immutability guards, the restore guarantees — assumes SQLite specifically; it
assumes a single writer per database and a transactional store.

No such deployment exists today, and none is planned; this is recorded so that
"Tangent is SQLite-only" is not mistaken for a product boundary. The product
boundary is in
[ADR 0005](./adr/0005-product-boundary-and-portfolio-composition.md), and it
says nothing about the store.

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
timestamp, schema version, size, SHA-256, integrity result, schema state, and
the preservation fingerprint of §4. The manifest deliberately holds the *base
name* of the source database and no path: it travels with the backup, and there
is no reason for it to carry the layout of the machine it came from.

The `sha256` is taken **after** the copy has been verified, and the order
matters: `VACUUM INTO` writes a rollback-journal database, and opening it to
verify it applies `PRAGMA journal_mode = WAL`, which rewrites the file header.
Digesting before that recorded a file that no longer existed, so checking a
backup against its own manifest reported *every* backup corrupt. Found and
fixed while re-verifying this section against the real artifact
(CW-20260905-0014); asserted by `TestManifestDigestDescribesTheFileOnDisk`.

`integrity_ok` means **the copy is undamaged**: pages, foreign keys, and every
immutability guard the copy's own schema defines. It does *not* mean "at this
binary's schema". When it is false, `damage` lists why, in the terms that name
the remedy — and the remedy for damage is always a different backup.
`schema_state` is the separate answer (`current`, `behind`, `ahead`, `dirty`,
`uninitialized`) with `schema_advice` saying what to do about it.

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

### Backing up before an upgrade

This is the ordinary case and the one the tooling used to fail. The source is
**older than the binary**, because you are taking the backup in order to
migrate — so the source is behind every time, by construction:

```
$ tangent --db-backup ~/tangent-backups/pre-upgrade.db   # add --db-drain if
                                                         # you can stop first
{ "schema_version": 10, "integrity_ok": true, "schema_state": "behind", ... }
tangent: db-backup: the backup is sound. the schema is at version 10 and this
         binary expects 12. This is not damage: a database taken before an
         upgrade is behind by design. Run `tangent --migrate-only` to bring it
         forward.
```

Exit code 0. *Verified* with a schema-12 binary against a real schema-10 copy
of the live database, in online mode with that installation still serving:
`integrity_ok: true`, `damage` empty, and a fingerprint measuring all five
properties over 87 interactions. The command exits non-zero only when the copy
is **damaged**, and then it names the damage.

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
   immutability guards intact **against the backup's own schema version**. A
   restore that overwrites a working database with a corrupt backup is worse
   than one that refuses.
2. **Close the handle**, then move the existing database and its `-wal`/`-shm`
   companions to `<database>.superseded-<timestamp>`. It is never deleted.
3. Copy the backup into place, reopen, and take a fresh fingerprint.
4. Compare the fingerprint property by property. Any difference is a failure.
5. On any failure after step 2, remove the partial copy and rename the
   superseded database back.

Restoring is a rollback in time, and that includes the erasure log: a restore
from a backup taken before an erasure brings the erased content back and
removes the `retention_operations` row that recorded its removal.

### Restoring a backup older than the binary

**It is allowed, and it is the normal rollback.** You took the backup before
migrating; restoring it puts you back before the migration, which is the whole
point of having taken it. Refusing would mean the recovery path this document
tells you to prepare does not work at the moment you need it.

The restore says where you landed and what to run next:

```
$ tangent --db-restore ~/tangent-backups/pre-upgrade.db --confirm
{ "schema_version": 10, "binary_schema_version": 12, "schema_state": "behind",
  "next_step": "the restored database is at schema 10 and this binary expects
                12. Run `tangent --migrate-only` before serving, or start the
                server, which migrates on boot.", "differences": [], ... }
```

Then `tangent --migrate-only`, and you are current again. *Verified end to end*
against a real schema-10 backup: restore exits 0 with no property differences,
`--migrate-only` brings it to 12, and `--db-check` reports `current` with all 87
interactions and every preservation property unchanged across the migration.

**What is still refused, and how to tell it apart.** A backup at a schema
*ahead* of this binary — use the newer binary. A backup with a **dirty** schema
— it was taken during a failed migration, which is damage, not age. A backup
missing an immutability guard its own schema defines. And a corrupt one:

```
tangent: db-restore: restore: backup "…" fails integrity check:
         *** in database main *** Tree 5 page 5 cell 14: Rowid … out of order …
```

That message and the `--migrate-only` one above are the two failures, and they
never read alike: damage names pages, guards, or a dirty schema and sends you
to another backup; age names a version and sends you to a migration. Both are
asserted in `internal/db/cross_schema_test.go`.

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

**The fingerprint is schema-aware.** Each property is measured over named
tables, and the table catalog is probed before anything is queried, so a
property whose tables the source schema predates is reported
`"applicable": false` rather than measured as zero or failing outright. The
fingerprint records the schema version it was taken at, so a reader knows which
sections were in scope. Two inapplicable measurements compare **equal** — a
copy of a database preserved exactly as much of a property as existed — while
an applicable measurement and an inapplicable one never do, and the difference
says which kind it is. *Verified* at every schema version this binary embeds:
`TestFingerprintIsMeasurableAtEverySchemaVersion` migrates a database to each
version in turn and asserts applicability against the catalog, so a future
migration that adds a column to a fingerprint query fails there rather than
against somebody's pre-upgrade backup.

**The immutability guards are compared against the source's own schema too.**
The reference inventory is built by migrating a throwaway in-memory database to
*that* version, not to HEAD. Measuring a schema-10 database against the
inventory at migration 12 reports the guards migrations 0011 and 0012 create as
missing, and "missing" is the word for a guard that was removed — not for one
that was never created.

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

`--db-check` reports `integrity_check`, `foreign_key_check`, migration status
and schema state, the immutability-guard inventory **against the schema the
database itself is at**, the storage footprint (page size and count, freelist,
WAL bytes, journal mode, foreign-key enforcement), who holds the lock, and the
recent erasure log.

Its exit code separates the two questions. **1 when the database is damaged**,
with the damage named; **1 when the schema is one this binary cannot work with**
— ahead, or never migrated — with the action named; **0 when it is sound**,
including when it is older than the binary, in which case the advice is printed
and the report carries `schema_state: "behind"`. *Verified* against a real
schema-10 database (exit 0), a dirty one (exit 1, "the schema is dirty at
version 12: a migration failed part-way"), one at a fabricated schema 99
(exit 1, "use that binary rather than this one"), and one with a corrupted page
(exit 1, "page-level corruption").

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

**`--db-repair` and `--retention-history` need the current schema**, and say so
rather than failing obscurely. *Verified* against a real schema-10 database:
repair exits 1 with `unfixable: ["the schema is at version 10 and this binary
expects 12. Run --migrate-only; repair does not migrate."]`, and
`--retention-history` reports that the erasure log arrives with migration 0012,
so an older database has no log rather than a damaged one. `--db-check` and
`--retention-plan` both run against an older schema and exit 0.

`--db-compact` runs `VACUUM` between two truncating checkpoints. It preserves
every row and every trigger; the fingerprint is asserted unchanged across it.

---

## 6. The immutability guards, and how they are suspended

Derived from the schema at migration 0012, not from the count ADR 0002 recorded
at `0a45caa`: **12 `BEFORE DELETE` guards**, out of a larger set of
immutability triggers. The ADR counted nine; migrations 0006, 0007, and 0012
added `terminal_outcome_acknowledgements_immutable_delete`,
`definition_manifests_immutable_delete`, and
`retention_operations_immutable_delete`. `internal/db/retention_test.go`
asserts the **12**, so a migration that adds an immutable table without telling
the retention path about it fails the build. The total trigger count is *not*
asserted anywhere and is deliberately not written here — an unguarded number in
prose is the drift this repository keeps paying for.

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

> **`draft_revisions` is empty in production.** `interaction.Service.SaveDraft`
> has no production caller, so nothing writes server-side drafts today; browser
> `localStorage` is the only running draft custody (`CW-20260905-0001`). The
> draft-deletion path above is implemented and tested, and it currently has
> nothing to delete. Do not read it as evidence that participant drafts are
> under durable custody — §9 covers what actually holds them.
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

One tool, read-only: **`tangent.retention_status`**. The size of the surrounding
tool surface is derived by `make smoke`, never written down here — see
[mcp-smoketest.md](mcp-smoketest.md).

It reports whether the guards are intact, the schema version and schema state
(`current`, `behind`, `ahead`, `dirty`, `uninitialized`), the storage state,
whether a process holds the lock and in what role, the windows in force, how many
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
- **The `/readyz` probe cannot say which database answered.** It is a note
  rather than a refusal for that reason (§1): a port is not a database, and a
  refusal that cannot tell them apart refuses the wrong thing. The ownership
  lock is what actually answers "is anyone using *this database*", and it
  covers every build since CW-20260825-0072. A Tangent old enough to take no
  lock is warned about, not blocked.
- **Windows** gets a refusal, not a lock. `internal/db` cross-compiles for it;
  nothing here has ever been run there.

## 10. Not implemented by this work

> **Reading these entries.** An entry naming a `CW-…` id is tracked work. An
> entry without one is **non-committed direction**: a constraint recorded so the
> next implementer does not have to rediscover it, not a promise that anyone
> will act on it. Nothing here is scheduled by virtue of being written down —
> the canonical limitation list is
> [`architecture.md`](architecture.md#current-limitations), and open work lives in Torque under
> project `PRJ-20260825-0002`.

- **The custody precedence engine** of ADR 0002 §3. Nothing writes a `custody`
  object into `interactions.policy`, so eligibility is computed from the host
  window alone and a caller or definition that asked for longer retention is not
  yet honored. `--retention-plan` says this in its own output rather than
  leaving it to be discovered.
- **The `custody_assurance: "legacy-unknown"` backfill** of §12.
- **The browser draft gate, key normalization, and one-time sweep** of §5.
- **The `external-reference` submission refusal** of §9.1.
