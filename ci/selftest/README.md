# pvr install / self-upgrade smoke test

A clean-container test that exercises the two real ways pvr lands on a machine
and keeps itself current, against the **live** GitLab registry and package
registry.

```sh
ci/selftest/run.sh
```

## What it covers

| Scenario | Path exercised |
| --- | --- |
| `install.sh` stable / develop channels | GitLab generic packages |
| release `051` client, `DistributionTag` = stable / develop / latest | docker-registry **manifest** path |
| `master` build, self-upgrade | GitLab generic **package** path |

The release-client cases are the regression guard for the incident where the
`:stable` and `:develop` docker tags were pushed as buildkit OCI **attestation
indexes**: an already-deployed pvr (which reads only plain
`manifest.v2+json`) got `image not found` and could not self-upgrade. The test
fails loudly on any `image not found` / `MANIFEST_UNKNOWN`, while retrying past
transient TLS/network blips on the docker bridge.

See also `ci/verify-plain-manifest.sh`, the CI guard that prevents a non-plain
channel manifest from being published in the first place.

## Notes

- Requires `docker` and outbound access to `gitlab.com`.
- Runs on the host architecture (amd64 or arm64); the docker image bundles
  every arch's binary, so self-upgrade extracts the right one via
  `GetPlatform()`.
