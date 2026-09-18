# Restore a Terraform file when its write fails

Resolves [issue #151](https://github.com/yesdevnull/tf-version-bump/issues/151): a failed write can truncate a Terraform file.

## Problem

`terraformFile.write` in `main.go` calls `os.WriteFile`, which opens the file with `O_TRUNC` before writing. If the write fails part way (ENOSPC, EDQUOT, EIO), the file is left empty or cut short. The command reports an error, but a truncated file usually still parses, so later entries in `processFiles`, the `-report-file` counts and later runs all treat the damaged file as legitimate.

## Goal and guarantee

A write that reports an error leaves the Terraform file with exactly the content it had before that write. This covers every failure the process observes: opening, writing, truncating and syncing the file.

Out of scope: a crash, `SIGKILL` or power loss part way through a write. In that case the file can hold a mixture of old and new content, and the backup described below remains beside it. That is strictly better than today, where the same event leaves a truncated file and no copy.

## Decision: back up, rewrite in place, restore on failure

The approach follows `gofmt -w` (`writeFile` in `src/cmd/gofmt/gofmt.go`), which backs up the original, rewrites it in place and writes the original bytes back if the rewrite fails.

The alternative the issue suggested, writing a temporary file and renaming it over the original, was rejected. A rename is atomic even across crashes, but it replaces the file's inode, which would:

- break hard links, so linked names silently keep old content;
- replace the owner, group, extended attributes and ACLs with those of the invoking user;
- replace a symlinked Terraform file with a regular file unless the link is resolved first, and file symlinks are matched today (see `file_selection_test.go`);
- let a read-only file be overwritten, because a rename needs only directory permission, contradicting the existing read-only write-failure tests;
- give the same file a new identity after every write, so `updateReport.fileIdentity`, which compares `os.Stat` results after each write, would count a block changed by two entries twice.

`terraform fmt -write` writes with `os.WriteFile(path, result, 0644)` in `internal/command/fmt.go`, so it keeps links and refuses read-only files, but it has the same truncation risk as this tool today. The chosen design keeps every behaviour users of `terraform fmt` already expect and removes the truncation risk for observed errors.

## Write sequence

`terraformFile.write` keeps its signature and its `failed to write file:` error prefix. It:

1. Opens the file with `os.O_RDWR` and no `O_TRUNC`. A read-only file therefore fails here with today's message, `failed to write file: open <name>: permission denied`, before anything is created. The permission bits are never changed, so passing them to the open call is no longer needed.
2. Reads the file's current content through the open handle. The backup therefore holds what is on disk at write time, not a copy held in memory since the parse.
3. Creates a backup with `os.CreateTemp(filepath.Dir(name), ".tf-version-bump-backup-*")`, writes the original bytes to it, syncs and closes it. The leading dot and the absence of a `.tf` suffix keep the backup out of every Terraform glob. If any of this fails, the partial backup is removed and the error is returned; the original has not been touched.
4. Writes the formatted bytes at offset 0, truncates to their length and syncs the file.
5. On success, closes the file and removes the backup.
6. If step 4 fails, writes the original bytes back at offset 0, truncates to their length and syncs. If that succeeds, the backup is removed and the write error is returned. If it fails, the backup is kept and the returned error names the write failure, the restore failure and the backup's path, so the user knows where the original content is.

The steps from 4 onwards are a small helper over a narrow interface (`WriteAt`, `Truncate` and `Sync`, all satisfied by `*os.File`). Tests drive it with a file whose writes fail after a chosen number of bytes, which exercises the real restore logic without simulating a full disk.

## Behaviour changes

- A writable Terraform file in a read-only directory now fails to write, because the backup cannot be created. The error names the cause, and the file is left untouched. `gofmt` has the same requirement.
- A failed write now leaves the file intact, so later entries in `processFiles`, which re-read the file after a failed write, see the original content rather than a truncated file.
- Hard links, symlinks, the owner, extended attributes, permission bits and read-only failures behave as before. `updateReport.fileIdentity` needs no change.

## Tests

New tests, written first under TDD, in `module_update_test.go` beside the existing write-failure and permission tests:

- A write that fails part way restores the original bytes exactly, returns an error with the `failed to write file:` prefix and leaves no backup in the directory.
- A write whose restore also fails keeps the backup, and the error names the backup's path; the backup holds the original bytes.
- A Terraform file in a read-only directory fails before the file changes, and leaves no backup. The test is skipped when running as root, which ignores directory permissions, as the existing read-only tests skip with `os.Geteuid() == 0`.
- A successful write through one hard-linked name is visible through the other.

The existing tests for read-only files (`module_update_test.go`, `terraform_version_test.go`, `provider_update_test.go`, `command_test.go`), preserved permission bits and hard-linked report counts must pass unchanged. After the TDD phase, a separate `test-cleanup` pass removes low-value tests.

## Documentation

- `CLAUDE.md`, gotcha "Don't run concurrent instances over the same files": replace "writes are not atomic" with the guarantee above: a failed write is rolled back, a crash part way can leave a mixed file with a `.tf-version-bump-backup-*` file beside it, and there is still no file locking.
- `docs/USAGE.md`, "File-writing behaviour": replace "Writes are not transactional" with the same guarantee, mention the backup file and the need for a writable directory, and keep the advice against concurrent runs.
- `command_test.go`, the comment on `TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk`: it says a failed write "may not have reached the file"; restate it as the file being restored to its content on disk, which is why later entries start from that content. The test's assertions do not change.

## Acceptance

A write that reports an error, for any reason, leaves the Terraform file exactly as it was, and no backup file remains unless restoring also failed. Every existing test passes unchanged, coverage stays at or above 90%, and the documented gotchas describe the new guarantee and its crash limitation.
