# Security policy

## Supported versions

Only the latest release gets security fixes. Upgrade before you report.

## Reporting a vulnerability

Please report it privately through GitHub:
**Security → Report a vulnerability** on this repository. Don't open a
public issue for a vulnerability.

Include the version (`marginalia version`), the mode you ran (`serve`, `mcp`,
`export`/`import` or `host`), and the smallest steps that reproduce it. Quote
the structure of the documents involved, never their content.

You'll get an answer within a week.

## Verifying a release

Every release archive and the published image carry a build provenance
attestation, and each archive ships an SPDX SBOM beside it:

```bash
gh attestation verify marginalia_<version>_linux_amd64.tar.gz --repo gruesomeparty/marginalia
gh attestation verify oci://ghcr.io/gruesomeparty/marginalia:<version> --repo gruesomeparty/marginalia
```
