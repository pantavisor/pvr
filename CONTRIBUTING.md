# Contributing to pvr

Write commit messages as [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
(`feat(device): add ssh`, `fix(libpvr): ...`, `docs: ...`). The changelog is
generated from them: `.chglog/config.yml` parses the `type(scope): subject`
header, and the type becomes the section title.

## Releases

Releases are plain three-digit tags cut from master (`054`, `055`).
The steps below use `056` as the new version.

1. Cut the release with the script. It needs Go and a clean master that
   matches origin. Start with a dry run to see the changelog section and the
   tag message:

   ```bash
   ci/release.sh 056 --dry-run
   ci/release.sh 056 --push
   ```

   The script adds the new section to `CHANGELOG.md` (using a pinned
   git-chglog), commits it as `chore: update CHANGELOG.md for release 056`,
   creates the annotated tag `056` with the section as its message, and
   pushes master and the tag. Without `--push` it stops after the tag and
   prints the push commands.

2. Wait for the tag pipeline (the `only: tags` jobs in `.gitlab-ci.yml`). It
   builds the binaries and docker images and uploads `pvr.056.src.tar.gz` and
   `pvr.056.vendor.tar.gz` to the GitLab package registry. Check that both
   downloads work (the script prints these commands):

   ```bash
   curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/056/pvr.056.src.tar.gz
   curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/056/pvr.056.vendor.tar.gz
   ```

3. Optional: add a GitLab release with longer notes (`049` and `052` have one):

   ```bash
   glab api -X POST projects/pantacor%2Fpvr/releases \
     -f tag_name=056 -f name=056 -f description="$(cat release-notes.md)"
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
git push origin master
# message: "056", a blank line, then the section without its anchor and heading
git tag -a --cleanup=verbatim 056 -F /tmp/056.msg
git push origin 056
```

`--cleanup=verbatim` matters: without it git drops the `###` lines. See
`git cat-file -p 054` for an example message.

## Rules

- Never delete, move or recreate a published tag. Pushing a tag again re-runs
  the pipeline and uploads the tarballs again, and their checksums can change.
  meta-pantavisor pins those checksums, so its build would break.
- If a tag was pushed without the annotated message, keep the tag and put the
  changelog into a GitLab release on it (`POST /projects/:id/releases`, as in
  step 3). This happened for `055`.
- Release candidates are tagged `NNN-rcN`, for example `049-rc1`.
