# Security policy

## Supported versions

`rc` has no long-term support branches. Security fixes go to the latest release.
Upgrade before reporting an issue.

## Reporting a vulnerability

Report suspected vulnerabilities privately:

- Open a [GitHub security advisory](https://github.com/chmouel/rcbin/security/advisories/new).
- Or email <chmouel@chmouel.com>.

Include the affected command, redacted configuration, and observed versus
expected behavior. I will coordinate a fix or mitigation before public
disclosure.

## Scope

`rc` runs local tools and reads or writes user-specified paths. Configuration and
host profiles are trusted input. Only run `rc` with files you control.
