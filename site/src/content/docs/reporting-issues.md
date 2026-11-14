---
title: Reporting bugs and security issues
description: How to report a bug, ask for a feature, or report a security vulnerability privately.
---

## Bugs and feature requests

Hammock's issues are on GitHub: [homegrownsw/hammock](https://github.com/homegrownsw/hammock/issues). Search first, in case someone has reported it already, then [open a new issue](https://github.com/homegrownsw/hammock/issues/new).

A good bug report says:

- **What you did, what you expected, and what happened instead**, step by step if you can.
- **Which version** of Hammock you're running (Hammock ▸ About Hammock, or `hmk status`), and of macOS. For the iPhone app, its version from TestFlight.
- **Where it happened:** the Mac app, the iPhone app, `hmk tui` or `hmk`, and whether a linked computer or a shared Group was involved.
- **For an agent problem,** which agent and its version, and whether you've installed hooks (`hmk hooks install`).
- **A screenshot or recording**, if it shows the problem.

:::caution[Check what you share]
Issues are public. Before you post a screenshot, a log or `hmk ls` output, check it for anything private: names, paths, hostnames, tokens, the contents of your panes. Use test data where you can.
:::

Ideas are welcome too. For anything bigger than a small improvement, describe what you'd like and why in an issue before anyone writes code: see [CONTRIBUTING.md](https://github.com/homegrownsw/hammock/blob/main/CONTRIBUTING.md).

## Security vulnerabilities

**Don't report security problems in a public issue.** Report them privately, through GitHub's **Report a vulnerability** button on the repository's [Security tab](https://github.com/homegrownsw/hammock/security). Say what an attacker needs (their position on the network, a paired device, a shared Group, code running as you) and what they get.

[SECURITY.md](https://github.com/homegrownsw/hammock/blob/main/SECURITY.md) describes what Hammock protects and what it doesn't. In short:

- Hammock runs as you, and so do your panes and agents.
- Remote access listens only on the networks you choose, and every device signs in with a key pinned when it was paired or linked.
- A person you let **Drive** a shared Group can run any command as you, and a linked computer has full control of yours.
- The limits on what agents may do are a guardrail for well-meaning agents, not a sandbox. For a real boundary, use your agent harness's own sandbox.
