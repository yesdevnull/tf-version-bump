#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPOSITORY_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
REPORT_SCRIPT="$SCRIPT_DIR/.github/scripts/report-state-branches.sh"
# shellcheck disable=SC2034 # Used by report and legacy subcommand tests added in later tasks.
REPORT_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-report.yml"
TEST_GIT=${TEST_GIT-git}
TEST_ROOT=$(mktemp -d)
TEST_ROOT=$(realpath "$TEST_ROOT")
# The report needs -audit-file, so the harness builds tf-version-bump from this checkout and a
# curl shim serves it at the release URL the script derives from this version.
TEST_VERSION=v9.9.9-report
TEST_ARCHIVE="$TEST_ROOT/tf-version-bump.tar.gz"
TEST_ARCHIVE_SHA256=""

FIXTURE_ROOT=""
FIXTURE_REMOTE=""
FIXTURE_SEED=""
FIXTURE_CONTROL=""
FIXTURE_RUNNER_TEMP=""
FIXTURE_OUTPUT=""
FIXTURE_BRANCH_ENTRIES='[]'
FIXTURE_BRANCH_COUNT=0
FIXTURE_LAST_OID=""

fail() {
    echo "FAIL: $*" >&2
    exit 1
}


cleanup() {
    chmod -R u+w "$TEST_ROOT" 2>/dev/null || true
    rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT


assert_silent_success() {
    local description=$1 stdout_file=$2 stderr_file=$3
    shift 3
    if ! "$@" >"$stdout_file" 2>"$stderr_file"; then
        fail "$description failed: $(<"$stderr_file")"
    fi
    [[ ! -s "$stdout_file" && ! -s "$stderr_file" ]] \
        || fail "$description emitted unexpected output: stdout=$(<"$stdout_file") stderr=$(<"$stderr_file")"
}


build_release_archive() {
    mkdir -p "$TEST_ROOT/release" "$TEST_ROOT/bin"
    go build -C "$REPOSITORY_ROOT" -ldflags "-X main.version=${TEST_VERSION#v}" \
        -o "$TEST_ROOT/release/tf-version-bump" .
    tar -czf "$TEST_ARCHIVE" -C "$TEST_ROOT/release" tf-version-bump
    TEST_ARCHIVE_SHA256=$(sha256sum "$TEST_ARCHIVE")
    TEST_ARCHIVE_SHA256=${TEST_ARCHIVE_SHA256%% *}
    cat >"$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output='' url=''
while [[ $# -gt 0 ]]; do
    case "$1" in
        --output) output=$2; shift 2 ;;
        -*) shift ;;
        *) url=$1; shift ;;
    esac
done
[[ "$url" == "${REPORT_TEST_EXPECTED_URL:?}" ]] || { echo "unexpected download: $url" >&2; exit 22; }
cp -- "${REPORT_TEST_ARCHIVE:?}" "$output"
EOF
    chmod +x "$TEST_ROOT/bin/curl"
}


fixture_commit() {
    local checkout=$1 message=$2
    shift 2
    "$TEST_GIT" -C "$checkout" \
        -c user.name='Report Test' \
        -c user.email='report-test@example.invalid' \
        commit "$@" -m "$message" >/dev/null
}


# An origin whose default branch holds the control config, a shallow control clone of it (as
# actions/checkout makes), and a seed repository that pushes state branches.
setup_report_fixture() {
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/fixture.XXXXXX")
    FIXTURE_REMOTE="$FIXTURE_ROOT/origin.git"
    FIXTURE_SEED="$FIXTURE_ROOT/seed"
    FIXTURE_CONTROL="$FIXTURE_ROOT/control"
    FIXTURE_RUNNER_TEMP="$FIXTURE_ROOT/runner-temp"
    FIXTURE_OUTPUT="$FIXTURE_RUNNER_TEMP/version-report"
    FIXTURE_BRANCH_ENTRIES='[]'
    mkdir -p "$FIXTURE_RUNNER_TEMP" "$FIXTURE_SEED/.github/tf-version-bump"
    "$TEST_GIT" init --quiet --bare --initial-branch=main "$FIXTURE_REMOTE"
    "$TEST_GIT" init --quiet --initial-branch=main "$FIXTURE_SEED"
    cat >"$FIXTURE_SEED/.github/tf-version-bump/test.yml" <<'EOF'
terraform_version: ">= 1.10"
providers:
  - name: aws
    version: "~> 6.0"
modules:
  - source: terraform-aws-modules/vpc/aws
    version: "5.0.0"
    ignore_modules:
      - "legacy_*"
EOF
    "$TEST_GIT" -C "$FIXTURE_SEED" add -- .github/tf-version-bump/test.yml
    fixture_commit "$FIXTURE_SEED" 'test: create report control fixture'
    "$TEST_GIT" -C "$FIXTURE_SEED" remote add origin "$FIXTURE_REMOTE"
    "$TEST_GIT" -C "$FIXTURE_SEED" push --quiet origin main
    "$TEST_GIT" clone --quiet --depth=1 "file://$FIXTURE_REMOTE" "$FIXTURE_CONTROL"
}


# Pushes a state branch whose tree is exactly what the writer function creates, and records it
# for collection as discovery would.
add_state_branch() {
    local branch=$1 writer=$2
    FIXTURE_BRANCH_COUNT=$((FIXTURE_BRANCH_COUNT + 1))
    "$TEST_GIT" -C "$FIXTURE_SEED" checkout --quiet --orphan "fixture-$FIXTURE_BRANCH_COUNT"
    "$TEST_GIT" -C "$FIXTURE_SEED" read-tree --empty
    find "$FIXTURE_SEED" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf -- {} +
    "$writer" "$FIXTURE_SEED"
    "$TEST_GIT" -C "$FIXTURE_SEED" add --all
    fixture_commit "$FIXTURE_SEED" "test: create $branch"
    FIXTURE_LAST_OID=$("$TEST_GIT" -C "$FIXTURE_SEED" rev-parse HEAD)
    "$TEST_GIT" -C "$FIXTURE_SEED" push --quiet origin "HEAD:refs/heads/$branch"
    FIXTURE_BRANCH_ENTRIES=$(jq -c --arg branch "$branch" --arg oid "$FIXTURE_LAST_OID" \
        '. + [{branch: $branch, base_oid: $oid}]' <<<"$FIXTURE_BRANCH_ENTRIES")
}


run_collect() {
    jq -n --argjson include "$FIXTURE_BRANCH_ENTRIES" '{include: $include}' \
        >"$FIXTURE_RUNNER_TEMP/branches.json"
    env PATH="$TEST_ROOT/bin:$PATH" \
        REPORT_TEST_EXPECTED_URL="https://github.com/yesdevnull/tf-version-bump/releases/download/$TEST_VERSION/tf-version-bump_${TEST_VERSION#v}_linux_x86_64.tar.gz" \
        REPORT_TEST_ARCHIVE="$TEST_ARCHIVE" \
        REPORT_POLICY_ID="${REPORT_POLICY_ID-nonproduction}" \
        REPORT_CONTROL_CHECKOUT="${REPORT_CONTROL_CHECKOUT-$FIXTURE_CONTROL}" \
        REPORT_CONFIG_PATH="${REPORT_CONFIG_PATH-.github/tf-version-bump/test.yml}" \
        REPORT_TERRAFORM_ROOTS="${REPORT_TERRAFORM_ROOTS-.}" \
        REPORT_BRANCHES="$FIXTURE_RUNNER_TEMP/branches.json" \
        REPORT_OUTPUT_DIR="${REPORT_OUTPUT_DIR-$FIXTURE_OUTPUT}" \
        REPORT_TF_VERSION_BUMP_VERSION="${REPORT_TF_VERSION_BUMP_VERSION-$TEST_VERSION}" \
        REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256="${REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256-$TEST_ARCHIVE_SHA256}" \
        RUNNER_TEMP="$FIXTURE_RUNNER_TEMP" \
        "$REPORT_SCRIPT" collect
}


write_alpha_branch() {
    cat >"$1/main.tf" <<'EOF'
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.2.0"
}

module "legacy_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.19.0"
}
EOF
    cat >"$1/versions.tf" <<'EOF'
terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
EOF
    printf 'provider "aws" {}\n' >"$1/providers.tf"
}


write_beta_branch() {
    cat >"$1/main.tf" <<'EOF'
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}
EOF
}


write_documented_beta_branch() {
    write_beta_branch "$1"
    mkdir "$1/docs"
    printf '# Notes\n' >"$1/docs/README.md"
}


test_collect_records_each_branch_root_and_audit() {
    setup_report_fixture
    add_state_branch state/nonproduction/alpha write_alpha_branch
    local alpha=$FIXTURE_LAST_OID
    add_state_branch state/staging/beta write_beta_branch
    local beta=$FIXTURE_LAST_OID

    assert_silent_success 'collecting two branches' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    jq -e --arg alpha "$alpha" --arg beta "$beta" '. == {
        policy: "nonproduction",
        roots: ["."],
        branches: [
          {branch: "state/nonproduction/alpha", commit: $alpha, error: null, roots: [
            {root: ".", exists: true, files: {"main.tf": true, "providers.tf": true}, audit: {
              schema_version: 1,
              terraform: [{file: "versions.tf", actual: ">= 1.10", expected: ">= 1.10", matches: true}],
              providers: [{file: "versions.tf", name: "aws", actual: "~> 5.0", expected: "~> 6.0", matches: false}],
              modules: [
                {file: "main.tf", name: "vpc", source: "terraform-aws-modules/vpc/aws",
                 actual: "4.2.0", expected: "5.0.0", matches: false, skip: null},
                {file: "main.tf", name: "legacy_vpc", source: "terraform-aws-modules/vpc/aws",
                 actual: "3.19.0", expected: "5.0.0", matches: false,
                 skip: {filter: "ignore_modules", values: ["legacy_*"]}}]}}]},
          {branch: "state/staging/beta", commit: $beta, error: null, roots: [
            {root: ".", exists: true, files: {"main.tf": true, "providers.tf": false}, audit: {
              schema_version: 1, terraform: [], providers: [],
              modules: [{file: "main.tf", name: "vpc", source: "terraform-aws-modules/vpc/aws",
                         actual: "5.0.0", expected: "5.0.0", matches: true, skip: null}]}}]}]}' \
        "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record both branches: $(<"$FIXTURE_OUTPUT/records.json")"
    [[ "$("$TEST_GIT" -C "$FIXTURE_CONTROL" worktree list | wc -l)" -eq 1 ]] \
        || fail 'collect left a branch worktree behind'
}


test_collect_records_missing_and_empty_roots() {
    setup_report_fixture
    add_state_branch state/staging/beta write_documented_beta_branch

    REPORT_TERRAFORM_ROOTS=$'.\ninfra\ndocs' assert_silent_success 'collecting three roots' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    jq -e '.roots == [".", "infra", "docs"] and (.branches[0].roots | map(del(.audit.modules)) == [
        {root: ".", exists: true, files: {"main.tf": true, "providers.tf": false},
         audit: {schema_version: 1, terraform: [], providers: []}},
        {root: "infra", exists: false, files: {"main.tf": false, "providers.tf": false}, audit: null},
        {root: "docs", exists: true, files: {"main.tf": false, "providers.tf": false},
         audit: {schema_version: 1, terraform: [], providers: []}}])
        and .branches[0].roots[2].audit.modules == []' \
        "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record missing and empty roots: $(<"$FIXTURE_OUTPUT/records.json")"
}


build_release_archive

if [[ $# -eq 0 ]]; then
    tests=(test_collect_records_each_branch_root_and_audit test_collect_records_missing_and_empty_roots)
else tests=("$@"); fi
for test_name in "${tests[@]}"; do
    "$test_name"
    printf 'PASS: %s\n' "$test_name"
done
