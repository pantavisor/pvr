# Remote builds (`pvr build`)

Development preview for Pantahub Builder. Submit a simulated execution or
package a digest-pinned registry image as a pvr export. Dockerfile builds,
Yocto images, reviewed production plans, and subscription admission remain
pending. Development executions are unbilled.

## Connect to the local HTTPS Hub

Start the cluster with `hub/tls-local.sh on`. The full Hub stack requires
valid credentials for its private GitLab container images. Configure pvr for
the API hostname printed by that command, and trust the local mkcert CA:

```sh
export PVR_BASEURL=https://api.<ip-dashed>.sslip.io
export SSL_CERT_FILE=/absolute/path/to/hub/api/.certs/rootCA.pem
export PVR_DISABLE_SELF_UPGRADE=1
pvr login
```

Existing pvr login/cache/refresh and `.netrc` authentication are reused. You
can instead provide a normal REST token through `PVR_ACCESSTOKEN`. It needs
`builds`, or both `builds.write` and `builds.readonly` scopes. MCP tokens cannot
be used on this REST surface. Build calls refuse redirects with credentials.
Global flags such as `--baseurl` and `--access-token` go before `build`.

## Start and inspect

```sh
pvr build start --project demo --target rpi4 \
  --mode simulated --limit-seconds 60 --simulated-seconds 8 \
  --idempotency-key demo-first-build

pvr build list
pvr build list --project demo
pvr build list --project demo --artifacts-only
pvr build usage
pvr build status <execution-id>
pvr build logs <execution-id>
```

`start` prints execution JSON including its `id`. Project and target are
currently metadata identifiers; no project creation is required. The limit
is a runner execution ceiling, excluding queue time. Simulation accepts
`--simulate-failure`; use a shorter execution limit than simulation duration
to exercise timeout.

Use a new idempotency key for a new build. Reuse the same key and exact
configuration after a lost submission response to retrieve the original
execution. If omitted, pvr generates a key and prints it to stderr before
submission so it can be recovered. Status and logs are JSON snapshots; log
snapshots contain the most recent 128 entries. Poll status until it becomes
`succeeded`, `failed`, `timed_out`, or `cancelled`.

## Download or cancel

```sh
pvr build download --output ./demo-artifacts.tar.gz <execution-id>
pvr build cancel <execution-id>
```

Download requires success, verifies SHA-256, and publishes a new file only
after verification. It never overwrites existing files or extracts archives.
An artifact is at most 5 GiB. Simulated builds produce `simulation.tar.gz`
(non-deployable); registry and git builds produce `<app>.pvrexport.tgz`, where
the app is named after the image (or the repository of a git build) unless
`--app` names it.

Cancellation returns execution JSON. A running execution may show
`cancel_requested: true` until the runner has stopped it; check status again.

## Registry-to-pvr exports

Enable the registry worker and `PH_BUILDER_ALLOW_LOCAL_DOCKER=true` as described
in the builder's `docs/development.md`, then submit:

```sh
pvr build start --project demo --target rpi4 --mode docker \
  --image 'registry.example.com/app@sha256:<64-lowercase-hex-digest>' \
  --platform linux/arm64 --limit-seconds 300 \
  --idempotency-key demo-registry-build
```

Supported platforms are `linux/arm64`, `linux/amd64` and `linux/arm/v6`. This imports an image;
it does not run the image or upload/build your current source directory.

## Private repositories (`pvr secret`)

Builds fetch private Git repositories with the account's secrets. A secret
names the hosts and paths it may be sent to; a build uses the one that covers
its repository most specifically, or the one named with `--secret`. Values are
write-only: give them on stdin or at the hidden prompt, never as a flag.

```sh
pvr secret add acme-github --type git --host github.com/acme < token.txt
pvr secret ls
pvr build start --project app --git-url https://github.com/acme/app --git-ref main
pvr build start --project app --git-url https://github.com/acme/app --secret acme-github
printf '%s\n' "$NEW_TOKEN" | pvr secret set acme-github --value
pvr secret rm acme-github
```

`--host` takes a host and optional path (`github.com`, `github.com/acme`,
`gitlab.com/group/sub`) and repeats. The builder sends the token only to the
repository URL being fetched, never into the Dockerfile build, and masks it in
build logs. The same secrets are managed in the web app under Secrets.

## Artifacts

A build's artifact is an object of your account, like the objects of a
revision: it counts against the account's storage and stays until deleted.
`pvr build download <id>` saves it under its own name (for example
`pv-mqtt-sdk.pvrexport.tgz`; `--output` to choose another, `--name` when a
build has several), checked against its sha256. It downloads from a signed
object URL, which never receives your credentials.

## One pipeline, several architectures

```sh
pvr build start --project mqtt-sdk \
  --git-url https://github.com/pantavisor/pv-mqtt-sdk.git \
  --git-ref <pinned-commit> --platform amd64,arm64,arm32v6 \
  --limit-seconds 600

pvr build status <pipeline-id>
pvr build logs <pipeline-id>
pvr build download --platform arm64 <pipeline-id>
pvr build cancel <pipeline-id>
```

One API request creates one parent pipeline with a child execution for each
architecture. Short names map to `linux/amd64`, `linux/arm64`, `linux/arm/v6`;
full platform strings also work. Duplicate targets are collapsed. The source
is resolved once, so every child uses the same commit. The execution limit
applies to each child, and parent usage is the sum of their measured seconds.

Use an account Git secret for the private repository. `pv-mqtt-sdk` already
cross-compiles these targets in its Dockerfile/build script. Other Dockerfiles
need native builders or emulation when running foreign-architecture commands.
Children queue behind a single runner; parallel execution needs more runner
capacity. Status includes each child's ID, architecture, status and usage.

Downloading a pipeline requires `--platform` when more than one artifact is
available. Default filenames include the architecture to avoid collisions.
A failed pipeline can still deliver successful children's artifacts. Cancelling
the parent requests cancellation for every unfinished child.

In the Hub, open **Builds → New pipeline**, enter comma-separated architectures,
and submit. The pipeline summary links to each child; its Artifacts tab offers
architecture-specific downloads.
