# agent-manager chart

One Deployment per role (`api`, `web`, `worker fetcher`, `worker scanner`, `worker notifier`),
the migrations as a `pre-install`/`pre-upgrade` Job, and optionally the second scan engine.

```bash
helm install hub deploy/helm/agent-manager -f my-values.yaml
task helm:lint   # lint + render with defaults and with ci/all-features-values.yaml
```

## Before you install

- **Postgres** with the roles and grants the migrations expect (`am_migrate`, `am_api`,
  `am_fetcher`, `am_scanner`, `am_notifier`, `am_queue`; see `deploy/local/postgres-init.sql`)
  and two databases: the application's and River's.
- **An S3-compatible bucket** with two keys: a writer for the fetcher, a reader for api and
  scanner.
- **An OIDC client** for api and web, and a session-mint secret the two share.

## Credentials

Each role has its own Secret built from `<role>.secretEnv`, or names one you manage with
`<role>.existingSecret`. Nothing is shared by template: hand each role only what `compose.yaml`
hands it. `web` and `notifier` refuse to render with a datastore or object-store credential
respectively; with `existingSecret` that check is yours.

| Role | Database user | Queue | Object-store key | Other |
| --- | --- | --- | --- | --- |
| api | `am_api` | `am_queue` | reader | OIDC client secret, mint secret |
| web | — | — | — | OIDC client secret, mint secret |
| fetcher | `am_fetcher` | `am_queue` | **writer** | GitHub token (optional) |
| scanner | `am_scanner` | `am_queue` | reader | — |
| notifier | `am_notifier` | `am_queue` | — | — |
| migrate | `am_migrate` | `am_queue` | — | — |

## CLI downloads

The Connect-the-CLI screen offers amctl for download from the hub itself when
`web.cliDist.volume` names a volume holding goreleaser's output for the CLI: the
`amctl_<version>_<os>_<arch>.tar.gz` archives and `checksums.txt`. Any pod volume source
works (a PersistentVolumeClaim, an OCI image volume, NFS). It is mounted read-only, and
the files must be readable by UID 10001. Fill it from a CLI release's assets. The archives
are not built into the image, because a darwin amctl needs CGO for the macOS keychain and
the image is a CGO-off Linux build. Leave the value empty and the dialog says no downloads
are served, but still shows the login command.

## Network

`networkPolicy.enabled` (on by default) denies everything, then allows: web → api; api and the
workers → Postgres and the object store; api and web → the identity provider; fetcher and
notifier → public addresses only (`networkPolicy.internetExcept`); scanner → the scan engine;
the scan engine → nothing, not even DNS, with ingress from the scanner alone. Postgres and the
object store default to private ranges; a public S3 endpoint needs its ranges added to
`networkPolicy.objectStore.to`. Metrics are reachable only from `networkPolicy.metricsFrom`.

## Observability

Every pod serves `/metrics` and `/healthz` on `metrics.port`; the workers' liveness probe is
that `/healthz`. `metrics.serviceMonitor.enabled` adds a ServiceMonitor (needs the Prometheus
operator CRDs). `tracing.otlpEndpoint` turns on OTLP/HTTP trace export for every role.

## Limits

- `web` with more than one replica needs `AGENT_MANAGER_WEB_COOKIE_KEY` (shared by every
  replica); without it each process signs the sign-in round trip with its own key.
- The scan engine image is the one `.github/workflows/release-skill-scanner-image.yaml`
  publishes; there is no upstream image.
- The pods run as UID 10001 with a read-only root filesystem and `/tmp` as an `emptyDir`.
