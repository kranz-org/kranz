# Security policy

## Supported versions

Security fixes are provided for the latest released minor version. Before the first stable release, only the latest published version is supported.

## Reporting a vulnerability

Do not open a public issue. Use GitHub's private security-advisory form at `https://github.com/kranz-org/kranz/security/advisories/new` and include the affected version, platform, reproduction details, and potential impact.

You should receive an acknowledgement within seven days. A coordinated disclosure date will be agreed after the issue is reproduced and a fix is available.

Kranz starts and stops local processes, reads project configuration and environment files, and can terminate an external process only after explicit confirmation. Reports involving command execution, configuration trust boundaries, PID ownership verification, or terminal escape handling are particularly valuable.
