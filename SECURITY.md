# Security policy

## Scope

This repository holds the source of a NAS service that runs on a single
private network. There is no hosted instance, no public endpoint and no
user base: nothing here is a service you can reach... :p 

So a vulnerability found in this code affects **whoever chooses to run it**

## Reporting

Use GitHub's **private vulnerability reporting** on this repository
(*Security* → *Report a vulnerability*). That keeps the report private until
there is something to say publicly, and it is the only channel — this file
deliberately carries no personal email address.

Please include what you would want to receive yourself: the affected file and
line, what an attacker gains, and the smallest input that shows it.

There is no bounty and no service-level commitment. This is one person's
project, answered as soon as it is read.

## What is already known, and is not a finding

Declared here so nobody spends an afternoon on something already written down:

- **The code assumes one specific home network** in roughly 15 places. On a
  different network some checks misclassify traffic — see the "honest status"
  section of the README. Known, scoped, not yet fixed.
- **There is no multi-user model.** Accounts exist, but the design assumes
  everyone with an account lives in the same house.
- **The service is reachable from the internet on purpose**, over TLS and
  through a tunnel, and the access-control panel is the compensating control.
  That it *can* be reached is a decision, not an oversight.

## What this project does take seriously

- **No secrets in the repository, history included.** Credentials are supplied
  by systemd's `LoadCredential=` on the node and by Windows Credential Manager
  on the client — never from a file in the tree. A gate checks every blob and
  every commit message before anything is published.
- **Failing loudly.** The recurring lesson in this codebase is that a check
  which cannot be performed must be reported as a failure, never as a pass.
- **Not shipping what it cannot verify.** The deployment gate refuses to build
  from a dirty tree, and verifies the hash of what actually landed on the node
  against what was compiled.
