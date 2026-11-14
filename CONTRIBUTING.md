# Contributing to pvr

Write commit messages as [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
(`feat(device): add ssh`, `fix(libpvr): ...`, `docs: ...`). The changelog is
generated from them: `.chglog/config.yml` parses the `type(scope): subject`
header, and the type becomes the section title.

## Releases

Releases are plain three-digit tags cut from main (`054`, `055`).
The steps below use `056` as the new version.

1. Cut the release with the script. It needs Go and a clean main that
   matches origin. Start with a dry run to see the changelog section and the
   tag message:

   ```bash
   ci/release.sh 056 --dry-run
   ci/release.sh 056 --push
   ```

   The script adds the new section to `CHANGELOG.md` (using a pinned
   git-chglog), commits it as `chore: update CHANGELOG.md for release 056`,
   creates the annotated tag `056` with the section as its message, and
   pushes main and the tag to GitHub, and to GitLab when a `gitlab` remote exists
   or `PVR_GITLAB_PUSH=1` is set (see [docs/github-migration.md](docs/github-migration.md)). Without `--push` it stops after the tag and
   prints the push commands.

2. Wait for the `release` workflow (`.github/workflows/release.yml`). It
   builds the binaries and creates the GitHub release `056` with the assets
   `pvr.056.<ARCH>.<EXT>`, including `pvr.056.src.tar.gz` and
   `pvr.056.vendor.tar.gz`; the CHANGELOG section is the release text. The
   GitLab tag pipeline (`.gitlab-ci.yml`) still uploads the same names to the
   GitLab package registry for pvr 059 and older. Check both:

   ```bash
   gh release view 056 -R pantavisor/pvr --json assets --jq '.assets[].name'
   curl -fsSLI https://github.com/pantavisor/pvr/releases/download/056/pvr.056.src.tar.gz
   curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/056/pvr.056.src.tar.gz
   ```

3. Optional: edit the GitHub release text for longer notes:

   ```bash
   gh release edit 056 -R pantavisor/pvr --notes-file release-notes.md
   ```

4. Bump pvr in meta-pantavisor: rename `recipes-pv/pvr/pvr_<old>.bb` to
   `pvr_056.bb` and update the three sha256 sums. The commit message names
   the main topic of the release and lists the upstream commits since the
   previous tag.

### Doing it by hand

If the script cannot be used, do the same steps manually:

```bash
git-chglog --next-tag 056 055.. # paste the output at the top of CHANGELOG.md
git commit -am "chore: update CHANGELOG.md for release 056"
git push origin main
# message: "056", a blank line, then the section without its anchor and heading
git tag -a --cleanup=verbatim 056 -F /tmp/056.msg
git push origin 056
```

`--cleanup=verbatim` matters: without it git drops the `###` lines. See
`git cat-file -p 054` for an example message.

## Rules

- Never delete, move or recreate a published tag. Pushing a tag again re-runs
  the pipelines and uploads the tarballs again, and their checksums can change.
  meta-pantavisor pins those checksums, so its build would break.
- If a tag was pushed without the annotated message, keep the tag and put the
  changelog into the GitHub release on it (`gh release edit`, as in
  step 3). This happened for `055`.
- Release candidates are tagged `NNN-rcN`, for example `049-rc1`.
