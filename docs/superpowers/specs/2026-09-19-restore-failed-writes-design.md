# Restore a Terraform file when its write fails

Resolves [issue #151](https://github.com/yesdevnull/tf-version-bump/issues/151): a failed write can truncate a Terraform file.

## Problem

`terraformFile.write` in `main.go` calls `os.WriteFile`, which opens the file with `O_TRUNC` before writing. If the write fails part way (ENOSPC, EDQUOT, EIO), the file is left empty or cut short. The command reports an error, but a truncated file usually still parses, so later entries, the `-report-file` counts and later runs all treat the damaged file as legitimate.

## Goal and guarantee

When a write reports an error, one of two things is true, and the error says which:

1. **Restored.** The Terraform file holds exactly the bytes it had before the write, and no backup remains.
2. **Backup kept.** The file could not be restored, or its final state is uncertain. The original bytes are preserved in a backup file whose full path the error names, and the file is treated as untrusted for the rest of the run (see [Untrusted files](#untrusted-files)).

This covers every failure the process observes: opening, reading, backing up, writing, truncating, syncing and closing.

Out of scope: a crash, `SIGKILL` or power loss part way through a write. The file can then hold a mixture of old and new content. The backup's data is synced but its directory entry is not, and it lives in the system temporary directory, which some systems clear at reboot, so after a power loss or reboot the backup may be gone too. Even so, this is no worse than today, where the same event leaves a truncated file and no copy.

## Decision: back up, rewrite in place, restore on failure

The approach follows `gofmt -w` (`writeFile` in `src/cmd/gofmt/gofmt.go`), which backs up the original, rewrites it in place and writes the original bytes back if the rewrite fails.

The alternative the issue suggested, writing a temporary file and renaming it over the original, was rejected. A rename is atomic even across crashes, but it replaces the file's inode, which would:

- break hard links, so linked names silently keep old content;
- replace the owner, group, extended attributes and ACLs with those of the invoking user;
- replace a symlinked Terraform file with a regular file unless the link is resolved first, and file symlinks are matched today (see `file_selection_test.go`);
- let a read-only file be overwritten, because a rename needs only directory permission, contradicting the existing read-only write-failure tests;
- give the same file a new identity after every write, so `updateReport.fileIdentity`, which compares `os.Stat` results after each write, would count a block changed by two entries twice.

`terraform fmt -write` writes with `os.WriteFile(path, result, 0644)` in `internal/command/fmt.go`, so it keeps links and refuses read-only files, but it has the same truncation risk as this tool today. The chosen design keeps every behaviour users of `terraform fmt` already expect and removes the truncation risk for observed errors.

Unlike `gofmt`, the backup goes in the system temporary directory rather than beside the file. The restore writes the original bytes back from memory, not by renaming the backup, so the backup need not share the file's filesystem. Keeping it out of the Terraform tree means no pattern (`*`, `**` or `**/*`) can select a leftover backup as an input on a later run, the tool needs no exclusion rule of its own, a read-only directory does not stop a writable file being updated, and symlinked inputs raise no question of which directory the backup belongs in.

## Write sequence

`terraformFile.write` keeps its signature and its `failed to write file:` error prefix. `<name>` below is the path as given, `<backup>` the backup's full path, and `<err>` the underlying error.

1. **Open.** Open the file through the rewrite hook (see [Test seam](#test-seam)), which calls `os.OpenFile(name, os.O_RDWR, 0)`: no `O_CREATE`, no `O_TRUNC`. On failure, return `failed to write file: <err>`. A read-only file therefore fails exactly as today (`failed to write file: open <name>: permission denied`) and nothing is created.
2. **Read.** Read the file's current content through the open handle, so the backup holds what is on disk at write time rather than a copy held in memory since the parse. On failure, close the handle and return `failed to write file: <err>`.
3. **Back up.** Create the backup with `os.CreateTemp("", "tf-version-bump-backup-" + filepath.Base(name) + "-*")`, which creates it with mode 0600 so other users cannot read it. Write the original bytes, `Sync` and `Close` it. On any failure, remove the partial backup, close the handle and return `failed to write file: cannot back up <name>: <err>`. The file has not been touched.
4. **Rewrite.** `WriteAt` the formatted bytes at offset 0, `Truncate` to their length and `Sync`.
5. **After a rewrite failure.**
   - If `WriteAt` wrote no bytes, the file is unchanged: remove the backup, close the handle and return `failed to write file: <err>`.
   - Otherwise restore: `WriteAt` the original bytes at offset 0, `Truncate` to their length and `Sync`. If that succeeds, remove the backup, close the handle and return `failed to write file: <err>; original content restored`.
   - If the restore fails, close the handle, keep the backup, mark the file untrusted and return `failed to write file: <err>; restoring the original also failed: <restore err>; original content is in <backup>`.
6. **Close.** After a successful rewrite, `Close` the handle. If `Close` fails, the synced content is most likely the new content, but its state cannot be confirmed, and reopening the file to restore it would add an untestable path. As `gofmt` does, do not restore: keep the backup, mark the file untrusted and return `failed to write file: <close err>; original content is in <backup>`.
7. **Clean up.** Remove the backup. If removal fails, print `Warning: could not remove backup <backup>: <err>` to stderr and return success, because the Terraform file was written correctly.

On failure paths the handle's own `Close` error is discarded (`_ = file.Close()`), as `preparedReportFile.publish` does, because a more specific error is already being returned.

Steps 4 and 5 are a helper, `rewriteContents(file rewritableFile, original, formatted []byte) (restored bool, err error)`, which returns whether the file holds its original bytes after a failure. `write` owns opening, reading, backing up, closing, removing the backup, marking the file untrusted and wording every error.

### Untrusted files

When a write keeps its backup, `write` records the file in a run-wide set of untrusted files, holding its `os.FileInfo` and backup path. The `os.FileInfo` is `terraformFile.info`, captured by the stat in `readTerraformFile`, so recording the file needs no further stat that could itself fail and leave a kept backup's file unmarked. `readTerraformFile` already stats each file before reading it; after that stat it checks the set with `os.SameFile`, as `updateReport.fileIdentity` does, and for an untrusted file returns `file left untrusted by an earlier failed write; original content is in <backup>` without reading it. Rewriting in place keeps the inode, so the check also catches the same file reached through a symlink or a hard link.

Every update path already logs a read error and counts it once per entry, so the Terraform-version pass, each provider pass and the module entries in `processFiles` all refuse the file for the rest of the run with no new plumbing, and the command exits 1. The audit (`audit.go`) reads and parses files itself and never writes, so it neither fills nor consults the set.

The set is package state guarded by a mutex. Tests that make a write keep its backup clear the set in `t.Cleanup`.

### Test seam

`rewritableFile` is the narrow interface the write needs: `Read`, `WriteAt`, `Truncate`, `Sync` and `Close`, all satisfied by `*os.File`. The file is opened through an unexported package variable, `openFileForRewrite`, defaulting to the `os.OpenFile` call in step 1 and guarded by `hookMu`, following the existing `exitFunc` and `fatalf` hooks (`main.go`, near the top).

Tests replace the hook with a wrapper around the real `*os.File` that fails a chosen call: the Nth `WriteAt` after writing only its first bytes (N = 2 fails the restore), a `Truncate`, a `Sync`, or the `Close`. Every call that is not chosen to fail goes through to the real file. The partial bytes really reach the disk, so every assertion concerns bytes on disk and the real restore logic, not the behaviour of a mock.

## Behaviour changes

- A write that fails part way now restores the file or keeps a backup, instead of leaving it truncated.
- A file whose write keeps a backup is refused for the rest of the run by every pass, instead of being re-read and updated again.
- A file deleted between the parse and the write now fails with `failed to write file: open <name>: no such file or directory`, instead of being recreated silently, because `O_CREATE` is dropped.
- The system temporary directory must be writable and have room for a copy of the file; if not, the write fails before the file is touched.
- `terraformFile.mode` is replaced by `info os.FileInfo`, because the permission bits are never changed and the untrusted-file set needs the file's identity. `readTerraformFile` keeps its `os.Stat`, which supplies `info`, performs the untrusted-file check and keeps the `failed to stat file:` prefix that `TestUpdateModuleVersionErrors` asserts for a missing file.
- Hard links, symlinks, owner, group, extended attributes, permission bits and read-only failures behave as before. `updateReport.fileIdentity` needs no change.

## Tests

New tests are written first, under TDD. Each points the system temporary directory at its own `t.TempDir()` with `t.Setenv`, setting `TMPDIR` for Unix and `TMP` and `TEMP` for Windows, where `os.TempDir` reads those instead, so it can assert exactly which backups exist. No test in the suite runs in parallel, so `t.Setenv` is allowed. Every error is asserted as an exact string.

In `module_update_test.go`, beside the existing write-failure tests:

- A write that fails part way restores the original bytes exactly, returns the `; original content restored` error and leaves no backup.
- A write whose new content is shorter than the original, and which fails at the `Truncate` or the `Sync` after a complete `WriteAt`, restores the original bytes exactly.
- A write that fails before writing any byte leaves the file unchanged, returns the plain `failed to write file: <err>` error and leaves no backup.
- A write whose restore also fails keeps exactly one backup holding the original bytes, returns the error naming that backup's path, and a later `readTerraformFile` of the same file, and of a hard link to it, returns the untrusted-file error.
- A `Close` failure after a successful rewrite keeps the backup, returns the error naming it, and leaves the file holding the new content.
- With the temporary directory set to a path that does not exist, the write returns the `cannot back up` error and the file is unchanged.
- When removing the backup fails after a successful write, the write succeeds and stderr holds exactly `Warning: could not remove backup <backup>: <err>`. The test double's successful `Close` makes the test's temporary directory read-only (mode 0o500, restored in `t.Cleanup`) before returning, so the removal fails. The test skips when running as root, which ignores directory permissions, and on Windows, where a read-only directory does not block removal.

In `command_test.go`, a config-mode run whose Terraform-version write keeps its backup logs the untrusted-file error for the file in each later provider and module entry, exits 1, and never writes the file again.

Awaiting Dan's decision: failures of the backup's own `Write`, `Sync` and `Close` share the `cannot back up` wording that the backup-creation test asserts, but provoking them needs a second hook around `os.CreateTemp`. Either add that hook and test each branch, or approve leaving these branches uncovered as an exception to the rule that tests cover all functionality.

The existing tests for read-only files (`module_update_test.go`, `terraform_version_test.go`, `provider_update_test.go`, `command_test.go`), preserved permission bits and hard-linked report counts, including the linked-name content check in `TestCommandReportCountsHardLinkedBlocksOnce`, must pass unchanged. After the TDD phase, a separate `test-cleanup` pass removes low-value tests, such as permission-bit tests that no longer exercise anything.

## Documentation

- `CLAUDE.md`, gotcha "Don't run concurrent instances over the same files": replace "writes are not atomic" with the two-tier guarantee, the untrusted-file rule, the crash and power-loss limitation, and the backup's location in the system temporary directory. There is still no file locking.
- `CLAUDE.md`, "Standard hclwrite pattern": replace "keeps the mode write must preserve" and "then the original permission bits" with the in-place rewrite and its restore.
- `AGENTS.md`, the hclwrite outline comment "Format and write back with the original permission bits": the same change.
- `docs/USAGE.md`, "File-writing behaviour": replace "The original permission bits are reused when the file is written" with the fact that files are rewritten in place, so permission bits, owner and links are untouched. Replace "Writes are not transactional" with the guarantee, the backup's location and name, what an error naming a backup means for the user, the need for a writable temporary directory, that pointing `TMPDIR` (or `TMP`/`TEMP` on Windows) inside the Terraform tree brings a leftover backup back into glob range, and the crash limitation. Keep the advice against concurrent runs.
- `main.go`: update the doc comments on `terraformFile` and `write`, and the `processFiles` comment "A failed write may not have reached the file…", which becomes: after a failed write the file is re-read, so later entries start from its original content when it was restored, and are refused by `readTerraformFile` when its backup was kept.
- `command_test.go`: the comment on `TestCommandConfigFailedWriteRestartsLaterEntriesFromDisk` says a failed write "may not have reached the file"; restate it as the file being restored, which is why later entries start from its content on disk. The test's assertions do not change: a read-only file fails at the open, before any backup.

## Acceptance

- A write that reports an error either leaves the Terraform file byte-for-byte as it was with no backup remaining, or keeps a backup of the original bytes, names that backup in the error, and every later read of the same file in the run is refused.
- The backup is created in `os.TempDir()`, never beside the Terraform file.
- Every existing test passes unchanged, and total coverage stays at or above 90%.
- `golangci-lint` passes.
- The documentation lists above describe the new behaviour.
