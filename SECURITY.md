# Security Policy

## Supported Versions

Only the latest released version is supported. Security fixes are not backported to older tags.

| Version  | Supported          |
| -------- | ------------------ |
| latest   | :white_check_mark: |
| < latest | :x:                |

## Reporting a Vulnerability

Please do **not** open a public GitHub issue for security vulnerabilities, and do not disclose
them publicly until they have been addressed.

1. **Report privately** via [GitHub Security Advisories](https://github.com/miikkak/mc-conn-poller/security/advisories/new) <!-- markdownlint-disable-line MD013 -->
2. **Include in your report:**
   - Description of the vulnerability
   - Steps to reproduce the issue
   - Potential impact
   - Suggested fix (if you have one)
3. **Response timeline:**
   - Acknowledgment within 48 hours
   - A detailed response within 7 days
   - A fix released as soon as possible

## Scope

This daemon opens no network listeners and accepts no external input at runtime. Its only
inputs are its local configuration (YAML file, `MCCP_` environment variables, CLI flags) and
the responses of the probed Minecraft servers. It makes outbound connections only: protocol
probes to the configured targets and HTTPS pings to the configured `ping_url`s.

The trust boundaries worth reporting against:

- **Probe responses.** A malicious or misbehaving target must not be able to crash the
  daemon, exhaust its memory (responses are length-capped), or hang it past the configured
  timeout.
- **Configuration.** Anyone who can write the config file controls where the daemon connects
  and what it pings; that is by design. Config parsing must still reject malformed input
  (e.g. non-HTTP(S) `ping_url`s) rather than act on it.
- **Credentials in logs and requests.** A `ping_url` acts as a bearer secret for its
  Healthchecks.io check. It should not appear in logs or be sent anywhere except the check
  itself. Keep the config file readable only by the user running the daemon.

## Security Scanning

- **Trivy** dependency vulnerability scanning
- **gitleaks** secret scanning (pre-commit, on every PR, on every push, and daily over full
  history)
- **Renovate** for automated dependency updates

Every pull request also gets an AI code review. It is a general correctness/quality review,
not a vulnerability scanner.

## Disclosure Policy

- Security issues are fixed in private before public disclosure
- After a fix is released, a security advisory is published
- Reporters are credited in the advisory unless they prefer anonymity
