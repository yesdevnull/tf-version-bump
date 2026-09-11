#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPOSITORY_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
REPORT_SCRIPT="$SCRIPT_DIR/.github/scripts/report-state-branches.sh"
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


test_workflow_reports_each_policy_read_only() {
    [[ -f "$REPORT_WORKFLOW" ]] || fail 'the report workflow does not exist'
    local callers
    callers=$(for policy in nonproduction production; do
        yq -o=json '.jobs.automation.with' "$SCRIPT_DIR/.github/workflows/tf-version-bump-$policy.yml"
    done | jq -s .)
    yq -o=json '.' "$REPORT_WORKFLOW" | jq -e --argjson callers "$callers" '
        .jobs.report as $job
        | ($job.steps | map({key: (.name // "checkout"), value: .}) | from_entries) as $steps
        | .permissions == {contents: "read"} and (.jobs | keys) == ["report"]
          and $job.permissions == {contents: "read"}
          and $job.if == "${{ github.ref == format('"'"'refs/heads/{0}'"'"', github.event.repository.default_branch) }}"
          and $job.strategy["fail-fast"] == false
          and ([$job.strategy.matrix.include[] | {policy, config_path, branch_prefixes, terraform_directories}]
               == [$callers[] | {policy: .automation_policy_id, config_path,
                                 branch_prefixes: .allowed_branch_prefixes, terraform_directories}])
          and [$job.steps[] | .name // "checkout"] == ["checkout", "Discover state branches", "Collect versions",
                                                      "Write version report", "Write legacy version report",
                                                      "Upload version reports"]
          and $steps["Discover state branches"].env.DISCOVERY_ALLOWED_PREFIXES == "${{ matrix.branch_prefixes }}"
          and $steps["Discover state branches"].env.DISCOVERY_POLICY_ID == "${{ matrix.policy }}"
          and $steps["Discover state branches"].env.DISCOVERY_MANUAL_PREFIX == ""
          and ($steps["Discover state branches"].run | contains(">\"$RUNNER_TEMP/branches.json\""))
          and $steps["Collect versions"].id == "collect"
          and $steps["Collect versions"].env.REPORT_BRANCHES == "${{ runner.temp }}/branches.json"
          and $steps["Collect versions"].env.REPORT_POLICY_ID == "${{ matrix.policy }}"
          and $steps["Collect versions"].env.REPORT_CONFIG_PATH == "${{ matrix.config_path }}"
          and $steps["Collect versions"].env.REPORT_TERRAFORM_ROOTS == "${{ matrix.terraform_directories }}"
          and $steps["Collect versions"].env.REPORT_TF_VERSION_BUMP_VERSION == $callers[0].tf_version_bump_version
          and $steps["Collect versions"].env.REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256 == $callers[0].tf_version_bump_archive_sha256
          and ([$steps["Collect versions", "Write version report", "Write legacy version report"].env.REPORT_OUTPUT_DIR]
               | unique == [$steps["Upload version reports"].with.path])
          and $steps["Write version report"].if == null
          and $steps["Write legacy version report"].if == "${{ !cancelled() && steps.collect.outcome == '"'"'success'"'"' }}"
          and $steps["Upload version reports"].if == $steps["Write legacy version report"].if
          and $steps["Upload version reports"].with["retention-days"] == 7
          and $steps["Upload version reports"].with["if-no-files-found"] == "error"
          and ([.. | strings | select(test("secrets\\."))] == [])
    ' >/dev/null || fail 'the report workflow does not wire discovery, collection and both reports read-only'
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


write_broken_branch() {
    printf 'module {\n' >"$1/main.tf"
}


write_symlinked_branch() {
    write_beta_branch "$1"
    mv "$1/main.tf" "$1/real.tf"
    ln -s real.tf "$1/main.tf"
}


write_escaping_root_branch() {
    write_beta_branch "$1"
    ln -s .. "$1/outside"
}


test_collect_records_unreadable_branches_and_continues() {
    setup_report_fixture
    add_state_branch state/staging/broken write_broken_branch
    add_state_branch state/staging/linked write_symlinked_branch
    add_state_branch state/staging/escaping write_escaping_root_branch
    local missing=0123456789abcdef0123456789abcdef01234567
    FIXTURE_BRANCH_ENTRIES=$(jq -c --arg oid "$missing" '. + [{branch: "state/staging/missing", base_oid: $oid}]' \
        <<<"$FIXTURE_BRANCH_ENTRIES")
    add_state_branch state/staging/beta write_beta_branch

    REPORT_TERRAFORM_ROOTS=$'.\noutside' assert_silent_success 'collecting unreadable branches' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    # The parse error must follow the root directly: collect strips the date and time Go's log
    # package puts before each CLI diagnostic, so the recorded error is stable between runs.
    jq -e --arg missing "$missing" '
        [.branches[].branch] == ["state/staging/broken", "state/staging/linked", "state/staging/escaping",
                                 "state/staging/missing", "state/staging/beta"]
        and (.branches[0].error | startswith("could not audit root .: Error auditing main.tf: failed to parse HCL"))
        and .branches[1].error == "root . contains a symlinked Terraform file"
        and .branches[2].error == "root outside resolves outside the checkout"
        and (.branches[3].error | startswith("could not fetch commit " + $missing))
        and all(.branches[0:4][]; .roots == [])
        and .branches[4].error == null
        and [.branches[4].roots[] | .exists] == [true, false]' \
        "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record the unreadable branches and continue: $(<"$FIXTURE_OUTPUT/records.json")"
}


test_collect_rejects_invalid_inputs() {
    setup_report_fixture
    add_state_branch state/staging/beta write_beta_branch
    local wrong_digest
    wrong_digest=$(printf 'a%.0s' {1..64})
    local -a cases=(
        "REPORT_TERRAFORM_ROOTS=../outside|Terraform roots must be non-empty relative paths without .."
        "REPORT_TERRAFORM_ROOTS=env-[12]|Terraform root env-[12] contains a glob character"
        "REPORT_CONFIG_PATH=/etc/hosts|config path must be relative and must not contain .."
        "REPORT_POLICY_ID=Bad|policy ID is invalid"
        "REPORT_TF_VERSION_BUMP_ARCHIVE_SHA256=$wrong_digest|tf-version-bump release archive checksum mismatch"
        "REPORT_OUTPUT_DIR=$FIXTURE_RUNNER_TEMP|output directory must be absolute and absent"
    )
    local entry assignment expected
    for entry in "${cases[@]}"; do
        assignment=${entry%%|*}
        expected=${entry#*|}
        if (export "${assignment?}"; run_collect) >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
            fail "collect accepted $assignment"
        fi
        [[ ! -s "$FIXTURE_ROOT/stdout" ]] || fail "collect printed output for $assignment"
        [[ "$(<"$FIXTURE_ROOT/stderr")" == "report error: $expected" ]] \
            || fail "collect did not report '$expected' for $assignment: $(<"$FIXTURE_ROOT/stderr")"
        [[ ! -e "$FIXTURE_OUTPUT/records.json" ]] || fail "collect wrote records for $assignment"
    done
}


test_collect_records_duplicate_roots_as_a_branch_error() {
    setup_report_fixture
    add_state_branch state/staging/beta write_beta_branch

    REPORT_TERRAFORM_ROOTS=$'.\n./' assert_silent_success 'collecting duplicate roots' \
        "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" run_collect

    jq -e '.branches == [{branch: "state/staging/beta", commit: .branches[0].commit,
        error: "root ./ duplicates another root", roots: []}]' "$FIXTURE_OUTPUT/records.json" >/dev/null \
        || fail "collect did not record duplicate roots as a branch error: $(<"$FIXTURE_OUTPUT/records.json")"
}


setup_records_fixture() {
    FIXTURE_ROOT=$(mktemp -d "$TEST_ROOT/records.XXXXXX")
    FIXTURE_OUTPUT="$FIXTURE_ROOT/version-report"
    mkdir "$FIXTURE_OUTPUT"
    : >"$FIXTURE_ROOT/summary.md"
}


# Four branches covering every improved-report status: alpha has a provider and module
# mismatch, a skipped module and a missing version; beta lacks providers.tf; gamma passes
# everything; delta could not be read.
write_report_records() {
    cat >"$FIXTURE_OUTPUT/records.json" <<'EOF'
{
  "policy": "nonproduction",
  "roots": ["."],
  "branches": [
    {"branch": "state/nonproduction/alpha", "commit": "1111111111111111111111111111111111111111", "error": null, "roots": [
      {"root": ".", "exists": true, "files": {"main.tf": true, "providers.tf": true}, "audit": {"schema_version": 1,
        "terraform": [{"file": "versions.tf", "actual": ">= 1.10", "expected": ">= 1.10", "matches": true}],
        "providers": [{"file": "versions.tf", "name": "aws", "actual": "~> 5.0", "expected": "~> 6.0", "matches": false}],
        "modules": [
          {"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "4.2.0", "expected": "5.0.0", "matches": false, "skip": null},
          {"file": "main.tf", "name": "legacy_vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "3.19.0", "expected": "5.0.0", "matches": false, "skip": {"filter": "ignore_modules", "values": ["legacy_*"]}},
          {"file": "storage.tf", "name": "logs", "source": "terraform-aws-modules/s3-bucket/aws", "actual": ">= 4.0, < 5.0", "expected": ">= 4.0, < 5.0", "matches": true, "skip": null},
          {"file": "storage.tf", "name": "assets", "source": "terraform-aws-modules/s3-bucket/aws", "actual": null, "expected": ">= 4.0, < 5.0", "matches": false, "skip": null}
        ]}}]},
    {"branch": "state/staging/beta", "commit": "2222222222222222222222222222222222222222", "error": null, "roots": [
      {"root": ".", "exists": true, "files": {"main.tf": true, "providers.tf": false}, "audit": {"schema_version": 1, "terraform": [], "providers": [],
        "modules": [{"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "5.0.0", "expected": "5.0.0", "matches": true, "skip": null}]}}]},
    {"branch": "state/staging/gamma", "commit": "3333333333333333333333333333333333333333", "error": null, "roots": [
      {"root": ".", "exists": true, "files": {"main.tf": true, "providers.tf": true}, "audit": {"schema_version": 1, "terraform": [], "providers": [],
        "modules": [{"file": "main.tf", "name": "vpc", "source": "terraform-aws-modules/vpc/aws", "actual": "5.0.0", "expected": "5.0.0", "matches": true, "skip": null}]}}]},
    {"branch": "state/staging/delta", "commit": "4444444444444444444444444444444444444444", "error": "could not fetch commit 4444444444444444444444444444444444444444", "roots": []}
  ]
}
EOF
}


run_report_subcommand() {
    env REPORT_OUTPUT_DIR="$FIXTURE_OUTPUT" GITHUB_STEP_SUMMARY="$FIXTURE_ROOT/summary.md" \
        "$REPORT_SCRIPT" "$1"
}


assert_file_content() {
    local description=$1 actual=$2 expected=$3
    diff -u "$expected" "$actual" >&2 || fail "$description differs from the expected content"
}


test_report_writes_the_improved_csv_and_summary() {
    setup_records_fixture
    write_report_records

    if run_report_subcommand report >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
        fail 'the report passed although a branch could not be read'
    fi
    [[ ! -s "$FIXTURE_ROOT/stdout" && "$(<"$FIXTURE_ROOT/stderr")" \
        == 'report error: 1 branch(es) could not be read; the version report lists them' ]] \
        || fail "the report did not name the unreadable branch count: $(<"$FIXTURE_ROOT/stderr")"

    cat >"$FIXTURE_ROOT/expected.csv" <<'EOF'
"status","branch","kind","subject","block","file","actual","expected","detail"
"PASS","state/nonproduction/alpha","root",".","","","","","found"
"PASS","state/nonproduction/alpha","file","main.tf","","main.tf","","","found"
"PASS","state/nonproduction/alpha","file","providers.tf","","providers.tf","","","found"
"PASS","state/nonproduction/alpha","terraform","required_version","","versions.tf",">= 1.10",">= 1.10",""
"FAIL","state/nonproduction/alpha","provider","aws","","versions.tf","~> 5.0","~> 6.0","version differs"
"FAIL","state/nonproduction/alpha","module","terraform-aws-modules/vpc/aws","vpc","main.tf","4.2.0","5.0.0","version differs"
"SKIP","state/nonproduction/alpha","module","terraform-aws-modules/vpc/aws","legacy_vpc","main.tf","3.19.0","5.0.0","skipped by ignore_modules (legacy_*)"
"PASS","state/nonproduction/alpha","module","terraform-aws-modules/s3-bucket/aws","logs","storage.tf",">= 4.0, < 5.0",">= 4.0, < 5.0",""
"FAIL","state/nonproduction/alpha","module","terraform-aws-modules/s3-bucket/aws","assets","storage.tf","",">= 4.0, < 5.0","no version attribute"
"PASS","state/staging/beta","root",".","","","","","found"
"PASS","state/staging/beta","file","main.tf","","main.tf","","","found"
"FAIL","state/staging/beta","file","providers.tf","","providers.tf","","","not found"
"PASS","state/staging/beta","module","terraform-aws-modules/vpc/aws","vpc","main.tf","5.0.0","5.0.0",""
"PASS","state/staging/gamma","root",".","","","","","found"
"PASS","state/staging/gamma","file","main.tf","","main.tf","","","found"
"PASS","state/staging/gamma","file","providers.tf","","providers.tf","","","found"
"PASS","state/staging/gamma","module","terraform-aws-modules/vpc/aws","vpc","main.tf","5.0.0","5.0.0",""
"ERROR","state/staging/delta","branch","","","","","","could not fetch commit 4444444444444444444444444444444444444444"
EOF
    assert_file_content 'the improved CSV' "$FIXTURE_OUTPUT/version-report.csv" "$FIXTURE_ROOT/expected.csv"

    cat >"$FIXTURE_ROOT/expected.md" <<'EOF'
## Version report (nonproduction)

Checked 4 branch(es): 4 FAIL, 12 PASS, 1 SKIP, 1 ERROR

### state/nonproduction/alpha: 3 FAIL, 5 PASS, 1 SKIP, 0 ERROR

| Status | Kind | Subject | Block | File | Actual | Expected | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| FAIL | provider | aws |  | versions.tf | &#126;&gt; 5.0 | &#126;&gt; 6.0 | version differs |
| FAIL | module | terraform-aws-modules/vpc/aws | vpc | main.tf | 4.2.0 | 5.0.0 | version differs |
| SKIP | module | terraform-aws-modules/vpc/aws | legacy&#95;vpc | main.tf | 3.19.0 | 5.0.0 | skipped by ignore&#95;modules (legacy&#95;&#42;) |
| FAIL | module | terraform-aws-modules/s3-bucket/aws | assets | storage.tf |  | &gt;= 4.0, &lt; 5.0 | no version attribute |

### state/staging/beta: 1 FAIL, 3 PASS, 0 SKIP, 0 ERROR

| Status | Kind | Subject | Block | File | Actual | Expected | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| FAIL | file | providers.tf |  | providers.tf |  |  | not found |

### state/staging/delta: 0 FAIL, 0 PASS, 0 SKIP, 1 ERROR

| Status | Kind | Subject | Block | File | Actual | Expected | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| ERROR | branch |  |  |  |  |  | could not fetch commit 4444444444444444444444444444444444444444 |

Branches where every check passed:

- state/staging/gamma (4 checks)
EOF
    assert_file_content 'the improved summary' "$FIXTURE_ROOT/summary.md" "$FIXTURE_ROOT/expected.md"
}


test_report_succeeds_when_every_branch_was_read() {
    setup_records_fixture
    write_report_records
    jq '.branches |= map(select(.error == null))' "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/readable.json"
    mv "$FIXTURE_ROOT/readable.json" "$FIXTURE_OUTPUT/records.json"

    assert_silent_success 'reporting readable branches' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand report
    grep -qxF 'Checked 3 branch(es): 4 FAIL, 12 PASS, 1 SKIP, 0 ERROR' "$FIXTURE_ROOT/summary.md" \
        || fail "the summary did not count the readable branches: $(<"$FIXTURE_ROOT/summary.md")"
}


# The audit records a non-literal expression as its source text, which can span lines.
test_report_keeps_multi_line_values_on_one_table_row() {
    setup_records_fixture
    write_report_records
    jq '.branches = [.branches[0] | .roots[0].audit.modules[0].actual = "try(\n  var.vpc_version,\n  \"5.0.0\"\n)"]' \
        "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/multi-line.json"
    mv "$FIXTURE_ROOT/multi-line.json" "$FIXTURE_OUTPUT/records.json"

    assert_silent_success 'reporting a multi-line value' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand report
    grep -qxF '| FAIL | module | terraform-aws-modules/vpc/aws | vpc | main.tf | try(<br>  var.vpc&#95;version,<br>  &quot;5.0.0&quot;<br>) | 5.0.0 | version differs |' \
        "$FIXTURE_ROOT/summary.md" || fail "the multi-line value broke its table row: $(<"$FIXTURE_ROOT/summary.md")"
}


test_legacy_writes_the_existing_report_format() {
    setup_records_fixture
    write_report_records

    assert_silent_success 'writing the legacy report' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand legacy

    cat >"$FIXTURE_ROOT/expected.csv" <<'EOF'
"Result","Test","Comment","State Branch"
"PASS","main.tf","found: main.tf","state/nonproduction/alpha"
"PASS","providers.tf","found: providers.tf","state/nonproduction/alpha"
"FAIL","terraform-aws-modules/vpc/aws","version mismatch: act. 4.2.0 exp. 5.0.0","state/nonproduction/alpha"
"FAIL","terraform-aws-modules/vpc/aws","version mismatch: act. 3.19.0 exp. 5.0.0","state/nonproduction/alpha"
"PASS","terraform-aws-modules/s3-bucket/aws","version matched: act. >= 4.0, < 5.0 exp. >= 4.0, < 5.0","state/nonproduction/alpha"
"FAIL","terraform-aws-modules/s3-bucket/aws","version mismatch: act. none exp. >= 4.0, < 5.0","state/nonproduction/alpha"
"PASS","main.tf","found: main.tf","state/staging/beta"
"FAIL","providers.tf","not found: providers.tf","state/staging/beta"
"PASS","terraform-aws-modules/vpc/aws","version matched: act. 5.0.0 exp. 5.0.0","state/staging/beta"
"PASS","main.tf","found: main.tf","state/staging/gamma"
"PASS","providers.tf","found: providers.tf","state/staging/gamma"
"PASS","terraform-aws-modules/vpc/aws","version matched: act. 5.0.0 exp. 5.0.0","state/staging/gamma"
EOF
    assert_file_content 'the legacy CSV' "$FIXTURE_OUTPUT/legacy-report.csv" "$FIXTURE_ROOT/expected.csv"

    cat >"$FIXTURE_ROOT/expected.md" <<'EOF'
## Legacy version report (nonproduction)

| Result | Test | Comment | State Branch |
| --- | --- | --- | --- |
| FAIL | terraform-aws-modules/vpc/aws | version mismatch: act. 4.2.0 exp. 5.0.0 | state/nonproduction/alpha |
| FAIL | terraform-aws-modules/vpc/aws | version mismatch: act. 3.19.0 exp. 5.0.0 | state/nonproduction/alpha |
| FAIL | terraform-aws-modules/s3-bucket/aws | version mismatch: act. none exp. &gt;= 4.0, &lt; 5.0 | state/nonproduction/alpha |
| FAIL | providers.tf | not found: providers.tf | state/staging/beta |
EOF
    assert_file_content 'the legacy summary' "$FIXTURE_ROOT/summary.md" "$FIXTURE_ROOT/expected.md"
}


test_legacy_reports_all_passed_and_missing_roots() {
    setup_records_fixture
    write_report_records
    jq '.branches |= map(select(.branch == "state/staging/gamma"))' "$FIXTURE_OUTPUT/records.json" \
        >"$FIXTURE_ROOT/gamma.json"
    mv "$FIXTURE_ROOT/gamma.json" "$FIXTURE_OUTPUT/records.json"

    assert_silent_success 'writing a passing legacy report' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand legacy
    [[ "$(<"$FIXTURE_ROOT/summary.md")" == $'## Legacy version report (nonproduction)\n\nAll 3 checks passed.' ]] \
        || fail "the passing legacy summary is wrong: $(<"$FIXTURE_ROOT/summary.md")"

    : >"$FIXTURE_ROOT/summary.md"
    jq '.branches[0].roots[0] = {root: ".", exists: false, files: {"main.tf": false, "providers.tf": false}, audit: null}' \
        "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/missing.json"
    mv "$FIXTURE_ROOT/missing.json" "$FIXTURE_OUTPUT/records.json"
    assert_silent_success 'writing a legacy report for a missing root' "$FIXTURE_ROOT/stdout" "$FIXTURE_ROOT/stderr" \
        run_report_subcommand legacy
    [[ "$(<"$FIXTURE_OUTPUT/legacy-report.csv")" == '"Result","Test","Comment","State Branch"
"FAIL","main.tf","not found: main.tf","state/staging/gamma"
"FAIL","providers.tf","not found: providers.tf","state/staging/gamma"' ]] \
        || fail "the missing-root legacy CSV is wrong: $(<"$FIXTURE_OUTPUT/legacy-report.csv")"
}


test_legacy_rejects_several_roots() {
    setup_records_fixture
    write_report_records
    jq '.roots = [".", "infra"]' "$FIXTURE_OUTPUT/records.json" >"$FIXTURE_ROOT/roots.json"
    mv "$FIXTURE_ROOT/roots.json" "$FIXTURE_OUTPUT/records.json"

    if run_report_subcommand legacy >"$FIXTURE_ROOT/stdout" 2>"$FIXTURE_ROOT/stderr"; then
        fail 'the legacy report accepted several roots'
    fi
    [[ ! -s "$FIXTURE_ROOT/stdout" \
        && "$(<"$FIXTURE_ROOT/stderr")" == 'report error: legacy report supports one Terraform root only' ]] \
        || fail "the legacy report did not reject several roots: $(<"$FIXTURE_ROOT/stderr")"
    [[ ! -e "$FIXTURE_OUTPUT/legacy-report.csv" && ! -s "$FIXTURE_ROOT/summary.md" ]] \
        || fail 'the legacy report wrote output for several roots'
}


build_release_archive

if [[ $# -eq 0 ]]; then
    tests=(test_collect_records_each_branch_root_and_audit test_collect_records_missing_and_empty_roots
        test_collect_records_unreadable_branches_and_continues test_collect_rejects_invalid_inputs
        test_collect_records_duplicate_roots_as_a_branch_error
        test_report_writes_the_improved_csv_and_summary test_report_succeeds_when_every_branch_was_read
        test_report_keeps_multi_line_values_on_one_table_row
        test_legacy_writes_the_existing_report_format test_legacy_reports_all_passed_and_missing_roots
        test_legacy_rejects_several_roots test_workflow_reports_each_policy_read_only)
else tests=("$@"); fi
for test_name in "${tests[@]}"; do
    "$test_name"
    printf 'PASS: %s\n' "$test_name"
done
