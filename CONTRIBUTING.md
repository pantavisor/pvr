# Contributing to pvr

Write commit messages as [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
(`feat(device): add ssh`, `fix(libpvr): ...`, `docs: ...`). The changelog is
generated from them: `.chglog/config.yml` parses the `type(scope): subject`
header, and the type becomes the section title.

## Releases

Releases are plain three-digit tags cut from master (`054`, `055`).
The steps below use `056` as the new version.

1. Add the changelog section on master. With
   [git-chglog](https://github.com/git-chglog/git-chglog) installed, from an
   up-to-date master:

   ```bash
   git-chglog --next-tag 056 -o CHANGELOG.md
   git add CHANGELOG.md
   git commit -m "chore: update CHANGELOG.md for release 056"
   git push origin master
   ```

   Check that the new section at the top of `CHANGELOG.md` looks like the
   earlier ones. Without git-chglog, write the section by hand in the same
   format.

2. Create an annotated tag. The first line of the message is the version,
   followed by a blank line and the new changelog section:

   ```bash
   { echo 056; echo; sed -n '1,/^<a name="055">/p' CHANGELOG.md | sed '$d'; } > /tmp/056.msg
   git tag -a 056 -F /tmp/056.msg
   ```

   Read `/tmp/056.msg` before tagging. See `git cat-file -p 054` for an example.

3. Push the tag:

   ```bash
   git push origin 056
   ```

4. Wait for the tag pipeline (the `only: tags` jobs in `.gitlab-ci.yml`). It
   builds the binaries and docker images and uploads `pvr.056.src.tar.gz` and
   `pvr.056.vendor.tar.gz` to the GitLab package registry. Check that both
   downloads work.

5. Optional: add a GitLab release with longer notes (`049` and `052` have one):

   ```bash
   glab api -X POST projects/pantacor%2Fpvr/releases \
     -f tag_name=056 -f name=056 -f description="$(cat release-notes.md)"
   ```

6. Bump pvr in meta-pantavisor: rename `recipes-pv/pvr/pvr_<old>.bb` to
   `pvr_056.bb` and update the three sha256 sums. The commit message names
   the main topic of the release and lists the upstream commits since the
   previous tag.

## Rules

- Never delete, move or recreate a published tag. Pushing a tag again re-runs
  the pipeline and uploads the tarballs again, and their checksums can change.
  meta-pantavisor pins those checksums, so its build would break.
- If a tag was pushed without the annotated message, keep the tag and put the
  changelog into a GitLab release on it (`POST /projects/:id/releases`, as in
  step 5). This happened for `055`.
- Release candidates are tagged `NNN-rcN`, for example `049-rc1`.
