# Security Policy

## Supported versions

`rc` is distributed without long-term support branches: only the latest released
version receives security fixes. Please upgrade to the most recent release before
reporting an issue.

## Reporting a vulnerability

Please report suspected vulnerabilities privately rather than opening a public
issue:

- Preferred: open a [GitHub security advisory](https://github.com/chmouel/rc/security/advisories/new)
  for this repository ("Report a vulnerability").
- Alternatively, email <chmouel@chmouel.com>.

Include enough detail to reproduce the issue: the affected command, your
configuration (with secrets redacted), and the observed versus expected
behavior. You will receive an acknowledgement, and a fix or mitigation will be
coordinated before any public disclosure.

## Scope and notes

`rc` orchestrates local tools on your workstation. By design it:

- runs external commands (`git`, `yadm`, lazygit, editors, backup/update tasks)
  defined in your configuration, and
- reads and writes files at user-specified paths.

Configuration is therefore trusted input. Only run `rc` with configuration you
control, and review overlays produced by `rc migrate` before relying on them.
