# Reviewing pull requests in Inbox

An agent sends a PR link with its summary and notes using the installed GitHub
plugin. The room shows the PR description, author, branches and retained head
commit. Review on GitHub opens the original PR in a new tab.

Approve records an approval on GitHub and leaves the room open. Merge offers
the methods enabled by the repository and saves the merge result in Inbox
history. Finish review saves an approval-only outcome. Dismiss closes the
Tangent request without changing the PR. Refresh reloads approval/merge status.

GitHub does not allow approving your own PR. The room explains this and keeps
Merge available when permitted. GitHub still enforces repository permissions,
checks and branch protections. If new commits arrive, ask the agent for a fresh
review; the old room cannot approve or merge the new commit.

Original PR content, agent notes and your confirmed response remain available
in Inbox history under Tangent's retention policy. History views are read-only.

See [the plugin guide](plugins/github-pr.md) for the agent call, installation,
authentication and testing. Tangent's renderer is application-neutral; the
plugin owns all GitHub access and credentials.
