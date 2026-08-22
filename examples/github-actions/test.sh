#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPOSITORY_ROOT=$(realpath "$SCRIPT_DIR/../..")
ACTIONLINT="$SCRIPT_DIR/../../scripts/run-actionlint.sh"
DISCOVER_SCRIPT="$SCRIPT_DIR/.github/scripts/discover-state-branches.sh"
PROCESS_SCRIPT="$SCRIPT_DIR/.github/scripts/process-state-branch.sh"
RECONCILE_SCRIPT="$SCRIPT_DIR/.github/scripts/reconcile-state-branch.sh"
RECONCILE_TEST="$SCRIPT_DIR/reconcile-test.sh"
REUSABLE_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-reusable.yml"
NONPRODUCTION_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-nonproduction.yml"
PRODUCTION_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-production.yml"
CONFIG_VALIDATION_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-config-validation.yml"
NONPRODUCTION_CONFIG="$SCRIPT_DIR/.github/tf-version-bump/nonproduction.yml"
PRODUCTION_CONFIG="$SCRIPT_DIR/.github/tf-version-bump/production.yml"
TEST_GIT=${TEST_GIT-git}

DISCOVERY_TMP_ROOT=""
DISCOVERY_REPO=""
DISCOVERY_REMOTE=""
DISCOVERY_CONTROL_OID=""

PROCESS_TMP_ROOT=""
PROCESS_CONTROL_CHECKOUT=""
PROCESS_TARGET_CHECKOUT=""
PROCESS_RUNNER_TEMP=""
PROCESS_PREPARATION_BUNDLE_DIR=""
PROCESS_VALIDATION_OUTCOME_DIR=""
PROCESS_VALIDATION_FIXTURE_ROOT=""
PROCESS_CONTAINER_ID=""
PROCESS_PATH_PREFIX=""
PROCESS_TEST_CALL_LOG=""
SUCCESSFUL_PREPARATION_READY=false

TEST_TMP_ROOT=$(mktemp -d)
TEST_TMP_ROOT=$(realpath "$TEST_TMP_ROOT")

TF_VERSION_BUMP_VERSION="v1.0.0-rc.9"
TF_VERSION_BUMP_ARCHIVE_SHA256="38428a229a77671fd192fd6a18f5d1f9c404b5557124883f04e6a8bec154b1d2"
TF_VERSION_BUMP_ARCHIVE_URL="https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0-rc.9/tf-version-bump_1.0.0-rc.9_linux_x86_64.tar.gz"
TF_VERSION_BUMP_PREFETCH_CONNECT_TIMEOUT_SECONDS=10
TF_VERSION_BUMP_PREFETCH_MAX_TIME_SECONDS=120
TERRAFORM_VERSION="1.15.5"
TERRAFORM_IMAGE="hashicorp/terraform:1.15.5@sha256:15bf5a08b1fb9c9747c8ff01098aeeefb4aec9a6c24eb13e7661bdf9447e4aee"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

sha256_file() {
    local digest
    digest=$(sha256sum "$1")
    printf '%s\n' "${digest%% *}"
}

fixture_commit() {
    local checkout=$1 author_name=$2 author_email=$3 message=$4
    shift 4
    "$TEST_GIT" -C "$checkout" \
        -c user.name="$author_name" \
        -c user.email="$author_email" \
        commit "$@" -m "$message" >/dev/null
}

# Writes an executable fake `terraform` at $1 that answers `version -json` with the pinned
# 1.15.5 fixture version (delayed by $3 seconds if given) and runs $2 -- a literal script body,
# captured by the caller from a quoted heredoc so its own `$`-references stay unevaluated until
# the stub itself runs -- for every other invocation.
write_terraform_stub() {
    local path=$1 body=$2 version_delay_seconds=${3-0}
    local sleep_line=""
    [[ "$version_delay_seconds" -eq 0 ]] || sleep_line="    sleep $version_delay_seconds"$'\n'
    local preamble
    preamble=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "version" && "${2:-}" == "-json" ]]; then
__SLEEP_LINE__    printf '%s\n' '{"terraform_version":"1.15.5"}'
    exit 0
fi
EOF
    )
    {
        printf '%s\n' "${preamble/__SLEEP_LINE__/$sleep_line}"
        printf '\n%s\n' "$body"
    } >"$path"
    chmod 755 "$path"
}


cleanup_discovery_repository() {
    if [[ -n "$DISCOVERY_TMP_ROOT" ]]; then
        rm -rf -- "$DISCOVERY_TMP_ROOT"
    fi
}

cleanup_processing_workspace() {
    if [[ -n "$PROCESS_TMP_ROOT" ]]; then
        chmod -R u+w "$PROCESS_TMP_ROOT" 2>/dev/null || true
        rm -rf -- "$PROCESS_TMP_ROOT"
        PROCESS_TMP_ROOT=""
    fi
}

cleanup_processing_container() {
    if [[ -n "$PROCESS_CONTAINER_ID" ]]; then
        docker rm --force "$PROCESS_CONTAINER_ID" >/dev/null 2>&1 || true
        PROCESS_CONTAINER_ID=""
    fi
}

ensure_processing_container() {
    if [[ -n "$PROCESS_CONTAINER_ID" ]]; then
        return
    fi

    # No --rm: a failed `apk add` must leave the container inspectable (State.Running and
    # `docker logs`) so the readiness loop can report why, instead of polling a vanished
    # container for two minutes. cleanup_processing_container force-removes it either way.
    PROCESS_CONTAINER_ID=$(docker run --detach \
        --platform linux/amd64 \
        --entrypoint /bin/sh \
        --volume "$SCRIPT_DIR:$SCRIPT_DIR:ro" \
        --volume "$TEST_TMP_ROOT:$TEST_TMP_ROOT" \
        "$TERRAFORM_IMAGE" \
        -c 'apk add --no-cache bash curl git jq coreutils && touch /tmp/harness-ready && exec tail -f /dev/null')

    local attempts=0
    until docker exec "$PROCESS_CONTAINER_ID" /bin/sh -c \
        'test -f /tmp/harness-ready' 2>/dev/null; do
        if [[ "$(docker inspect --format='{{.State.Running}}' "$PROCESS_CONTAINER_ID" 2>/dev/null)" \
            != true ]]; then
            fail "pinned Terraform preparation container exited before becoming ready: $(docker logs "$PROCESS_CONTAINER_ID" 2>&1)"
        fi
        attempts=$((attempts + 1))
        if [[ "$attempts" -ge 120 ]]; then
            fail "pinned Terraform preparation container did not become ready"
        fi
        sleep 1
    done
    docker exec "$PROCESS_CONTAINER_ID" /bin/bash -c \
        'command -v curl >/dev/null && command -v jq >/dev/null && command -v timeout >/dev/null'
}

setup_processing_workspace() {
    cleanup_processing_workspace
    unset PROCESS_CONFIG_PATH PROCESS_TERRAFORM_ROOTS RUNNER_TEMP
    unset PROCESS_PREPARATION_DEADLINE_EPOCH
    unset PROCESS_VALIDATION_DEADLINE_EPOCH
    unset PROCESS_RUN_ID PROCESS_RUN_ATTEMPT PROCESS_AUTOMATION_POLICY_ID
    unset PROCESS_CONTROL_OID PROCESS_STATE_BRANCH PROCESS_BASE_OID PROCESS_REF_HASH
    unset PROCESS_TF_VERSION_BUMP_VERSION PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256
    unset PROCESS_TERRAFORM_VERSION
    unset TF_CLI_CONFIG_FILE
    PROCESS_PATH_PREFIX=""
    PROCESS_TEST_CALL_LOG=""
    SUCCESSFUL_PREPARATION_READY=false
    PROCESS_TMP_ROOT=$(mktemp -d "$TEST_TMP_ROOT/processing.XXXXXX")
    PROCESS_TMP_ROOT=$(realpath "$PROCESS_TMP_ROOT")
    PROCESS_CONTROL_CHECKOUT="$PROCESS_TMP_ROOT/control"
    PROCESS_TARGET_CHECKOUT="$PROCESS_TMP_ROOT/target"
    PROCESS_RUNNER_TEMP="$PROCESS_TMP_ROOT/runner-temp"

    mkdir -p \
        "$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump" \
        "$PROCESS_TARGET_CHECKOUT/root" \
        "$PROCESS_RUNNER_TEMP"
    printf '%s\n' 'terraform_version: ">= 1.15.0"' \
        >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/root/main.tf"

    "$TEST_GIT" init --initial-branch=main "$PROCESS_CONTROL_CHECKOUT" >/dev/null
    "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- \
        ".github/tf-version-bump/test.yml"
    fixture_commit "$PROCESS_CONTROL_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: create processing control fixture"
    "$TEST_GIT" init --initial-branch=main "$PROCESS_TARGET_CHECKOUT" >/dev/null
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- \
        "root/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: create processing fixture"

    PROCESS_STATE_BRANCH="state/nonproduction/example-thing"
    PROCESS_PREPARATION_BUNDLE_DIR="$PROCESS_RUNNER_TEMP/preparation-bundle"
    PROCESS_VALIDATION_OUTCOME_DIR="$PROCESS_RUNNER_TEMP/validation-outcome"
}

build_validation_provider_fixture() {
    local fixture_source="$SCRIPT_DIR/test-fixtures/test-provider"
    PROCESS_VALIDATION_FIXTURE_ROOT="$PROCESS_RUNNER_TEMP/validation-test-provider-v1"
    local mirror_directory="$PROCESS_VALIDATION_FIXTURE_ROOT/provider-mirror/registry.terraform.io/yesdevnull/test/0.1.0/linux_amd64"
    mkdir -p "$mirror_directory"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -C "$fixture_source" \
        -o "$mirror_directory/terraform-provider-test_v0.1.0_x5" .
    chmod 555 "$mirror_directory/terraform-provider-test_v0.1.0_x5"
    cat >"$PROCESS_VALIDATION_FIXTURE_ROOT/terraform.rc" <<EOF
provider_installation {
  filesystem_mirror {
    path    = "$PROCESS_VALIDATION_FIXTURE_ROOT/provider-mirror"
    include = ["registry.terraform.io/yesdevnull/test"]
  }
  direct {
    exclude = ["registry.terraform.io/yesdevnull/test"]
  }
}
EOF
    chmod -R a-w "$PROCESS_VALIDATION_FIXTURE_ROOT"
    TF_CLI_CONFIG_FILE="$PROCESS_VALIDATION_FIXTURE_ROOT/terraform.rc"
}

write_validation_provider_configuration() {
    cat >"$PROCESS_TARGET_CHECKOUT/root/main.tf" <<'EOF'
terraform {
  required_version = ">= 1.0"

  required_providers {
    test = {
      source  = "yesdevnull/test"
      version = "0.1.0"
    }
  }
}
EOF
}

configure_validation_provider_base() {
    build_validation_provider_fixture
    write_validation_provider_configuration

    local lock_data="$PROCESS_RUNNER_TEMP/lock-data"
    mkdir -m 700 "$lock_data"
    docker run --rm \
        --platform linux/amd64 \
        --user "$(id -u):$(id -g)" \
        --volume "$PROCESS_TARGET_CHECKOUT:/workspace" \
        --volume "$lock_data:/terraform-data" \
        --volume "$PROCESS_VALIDATION_FIXTURE_ROOT:$PROCESS_VALIDATION_FIXTURE_ROOT:ro" \
        --env TF_DATA_DIR=/terraform-data \
        --env "TF_CLI_CONFIG_FILE=$PROCESS_VALIDATION_FIXTURE_ROOT/terraform.rc" \
        --entrypoint terraform \
        "$TERRAFORM_IMAGE" \
        -chdir=/workspace/root init -backend=false -input=false -no-color \
        >"$PROCESS_TMP_ROOT/lock-init.stdout" 2>"$PROCESS_TMP_ROOT/lock-init.stderr"
    rm -rf "$lock_data"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- \
        "root/main.tf" "root/.terraform.lock.hcl"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add validation provider"
}

configure_validation_provider_base_without_lock() {
    build_validation_provider_fixture
    write_validation_provider_configuration
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" \
        "processing-test@example.invalid" "test: add lock-free validation provider"
}

create_validation_candidate_bundle() {
    local add_provider_lock=${1-false}
    local candidate_checkout="$PROCESS_TMP_ROOT/candidate"
    "$TEST_GIT" clone --quiet "$PROCESS_TARGET_CHECKOUT" "$candidate_checkout"
    sed -i.bak 's/>= 1\.0/>= 1.15.0/' "$candidate_checkout/root/main.tf"
    rm "$candidate_checkout/root/main.tf.bak"
    if [[ "$add_provider_lock" == "true" ]]; then
        local lock_data="$PROCESS_RUNNER_TEMP/candidate-lock-data"
        mkdir -m 700 "$lock_data"
        docker run --rm \
            --platform linux/amd64 \
            --user "$(id -u):$(id -g)" \
            --volume "$candidate_checkout:/workspace" \
            --volume "$lock_data:/terraform-data" \
            --volume "$PROCESS_VALIDATION_FIXTURE_ROOT:$PROCESS_VALIDATION_FIXTURE_ROOT:ro" \
            --env TF_DATA_DIR=/terraform-data \
            --env "TF_CLI_CONFIG_FILE=$PROCESS_VALIDATION_FIXTURE_ROOT/terraform.rc" \
            --entrypoint terraform \
            "$TERRAFORM_IMAGE" \
            -chdir=/workspace/root init -backend=false -input=false -no-color \
            >"$PROCESS_TMP_ROOT/candidate-lock-init.stdout" \
            2>"$PROCESS_TMP_ROOT/candidate-lock-init.stderr"
        rm -rf "$lock_data"
        "$TEST_GIT" -C "$candidate_checkout" add -N -- "root/.terraform.lock.hcl"
    fi

    mkdir -m 700 "$PROCESS_PREPARATION_BUNDLE_DIR"
    "$TEST_GIT" -C "$candidate_checkout" diff --binary --full-index --no-color \
        >"$PROCESS_PREPARATION_BUNDLE_DIR/update.patch"
    local patch_sha256
    patch_sha256=$(sha256_file "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch")
    local main_sha256
    main_sha256=$(sha256_file "$candidate_checkout/root/main.tf")
    local lock_sha256=""
    if [[ "$add_provider_lock" == "true" ]]; then
        lock_sha256=$(sha256_file "$candidate_checkout/root/.terraform.lock.hcl")
    fi
    local ref_hash
    ref_hash=$(processing_ref_hash)
    jq -n \
        --arg run_id "123456" \
        --arg run_attempt "2" \
        --arg policy "nonproduction" \
        --arg control_oid "$(processing_control_oid)" \
        --arg state_branch "$PROCESS_STATE_BRANCH" \
        --arg base_oid "$(processing_base_oid)" \
        --arg ref_hash "$ref_hash" \
        --arg patch_sha256 "$patch_sha256" \
        --arg main_sha256 "$main_sha256" \
        --arg lock_sha256 "$lock_sha256" \
        --arg tf_version_bump_version "$TF_VERSION_BUMP_VERSION" \
        --arg tf_version_bump_archive_sha256 "$TF_VERSION_BUMP_ARCHIVE_SHA256" \
        '{schema_version: 2,
          run_id: $run_id,
          run_attempt: $run_attempt,
          automation_policy_id: $policy,
          control_oid: $control_oid,
          state_branch: $state_branch,
          base_oid: $base_oid,
          ref_hash: $ref_hash,
          config_path: ".github/tf-version-bump/test.yml",
          tools: {
            tf_version_bump: {
              version: $tf_version_bump_version,
              archive_sha256: $tf_version_bump_archive_sha256
            },
            terraform: {version: "1.15.5"}
          },
          roots: [{path: "root"}],
          artifact_name: ("preparation-123456-2-nonproduction-" + $ref_hash),
          classification: "success",
          terraform_fmt: false,
          updates: {
            module_blocks_updated: 0,
            provider_blocks_updated: 0,
            changed_files:
              (if $lock_sha256 == "" then [] else
                 [{path: "root/.terraform.lock.hcl", mode: "100644", sha256: $lock_sha256}]
               end) +
              [{path: "root/main.tf", mode: "100644", sha256: $main_sha256}],
            patch_sha256: $patch_sha256
          },
          formatting: {ran: false, changed_files: []},
          final_changed_files:
            (if $lock_sha256 == "" then [] else
               [{path: "root/.terraform.lock.hcl", mode: "100644", sha256: $lock_sha256}]
             end) +
            [{path: "root/main.tf", mode: "100644", sha256: $main_sha256}]}' \
        >"$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json"
    chmod 444 \
        "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json"

    local validation_checkout="$PROCESS_TMP_ROOT/validation"
    "$TEST_GIT" clone --quiet "$PROCESS_TARGET_CHECKOUT" "$validation_checkout"
    PROCESS_TARGET_CHECKOUT="$validation_checkout"
}

# Populates the PROCESSING_SHARED_DOCKER_ENV array with the `docker exec --env ...` pairs common
# to both run_processing_prepare and run_processing_validate (run identity, immutable state-branch
# lineage, the pinned Terraform version, RUNNER_TEMP, and PATH).
processing_shared_docker_env() {
    PROCESSING_SHARED_DOCKER_ENV=(
        --env "PROCESS_RUN_ID=${PROCESS_RUN_ID-123456}"
        --env "PROCESS_RUN_ATTEMPT=${PROCESS_RUN_ATTEMPT-2}"
        --env "PROCESS_AUTOMATION_POLICY_ID=${PROCESS_AUTOMATION_POLICY_ID-nonproduction}"
        --env "PROCESS_CONTROL_OID=${PROCESS_CONTROL_OID-$(processing_control_oid)}"
        --env "PROCESS_STATE_BRANCH=${PROCESS_STATE_BRANCH:?}"
        --env "PROCESS_BASE_OID=${PROCESS_BASE_OID-$(processing_base_oid)}"
        --env "PROCESS_REF_HASH=${PROCESS_REF_HASH-$(processing_ref_hash)}"
        --env "PROCESS_TERRAFORM_VERSION=${PROCESS_TERRAFORM_VERSION-$TERRAFORM_VERSION}"
        --env "RUNNER_TEMP=${RUNNER_TEMP-$PROCESS_RUNNER_TEMP}"
        --env "PATH=${PROCESS_PATH_PREFIX:+$PROCESS_PATH_PREFIX:}/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    )
}

run_processing_validate() {
    ensure_processing_container
    processing_shared_docker_env
    docker exec \
        --user "$(id -u):$(id -g)" \
        --env GIT_CONFIG_COUNT=1 \
        --env GIT_CONFIG_KEY_0=safe.directory \
        --env "GIT_CONFIG_VALUE_0=$PROCESS_TARGET_CHECKOUT" \
        --env "PROCESS_TARGET_CHECKOUT=$PROCESS_TARGET_CHECKOUT" \
        --env "PROCESS_PREPARATION_BUNDLE_DIR=$PROCESS_PREPARATION_BUNDLE_DIR" \
        --env "PROCESS_VALIDATION_OUTCOME_DIR=$PROCESS_VALIDATION_OUTCOME_DIR" \
        --env "PROCESS_VALIDATION_DEADLINE_EPOCH=${PROCESS_VALIDATION_DEADLINE_EPOCH-$(($(date +%s) + 1200))}" \
        "${PROCESSING_SHARED_DOCKER_ENV[@]}" \
        --env "TF_CLI_CONFIG_FILE=${TF_CLI_CONFIG_FILE-}" \
        "$PROCESS_CONTAINER_ID" \
        /bin/bash "$PROCESS_SCRIPT" validate
}

processing_control_oid() {
    "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" rev-parse HEAD
}

processing_base_oid() {
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" rev-parse HEAD
}

processing_ref_hash() {
    local ref_hash
    ref_hash=$(printf '%s' "refs/heads/$PROCESS_STATE_BRANCH" | sha256sum)
    printf '%s\n' "${ref_hash%% *}"
}

run_processing_prepare() {
    ensure_processing_container
    processing_shared_docker_env
    docker exec \
        --user "$(id -u):$(id -g)" \
        --env GIT_CONFIG_COUNT=2 \
        --env GIT_CONFIG_KEY_0=safe.directory \
        --env "GIT_CONFIG_VALUE_0=$PROCESS_CONTROL_CHECKOUT" \
        --env GIT_CONFIG_KEY_1=safe.directory \
        --env "GIT_CONFIG_VALUE_1=$PROCESS_TARGET_CHECKOUT" \
        --env "PROCESS_CONTROL_CHECKOUT=$PROCESS_CONTROL_CHECKOUT" \
        --env "PROCESS_TARGET_CHECKOUT=$PROCESS_TARGET_CHECKOUT" \
        --env "PROCESS_CONFIG_PATH=${PROCESS_CONFIG_PATH-.github/tf-version-bump/test.yml}" \
        --env "PROCESS_TERRAFORM_ROOTS=${PROCESS_TERRAFORM_ROOTS-root}" \
        "${PROCESSING_SHARED_DOCKER_ENV[@]}" \
        --env "PROCESS_TF_VERSION_BUMP_VERSION=${PROCESS_TF_VERSION_BUMP_VERSION-$TF_VERSION_BUMP_VERSION}" \
        --env "PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256=${PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256-$TF_VERSION_BUMP_ARCHIVE_SHA256}" \
        --env "PROCESS_PREPARATION_DEADLINE_EPOCH=${PROCESS_PREPARATION_DEADLINE_EPOCH-$(($(date +%s) + 1200))}" \
        --env "PROCESS_PREPARATION_BUNDLE_DIR=${PROCESS_PREPARATION_BUNDLE_DIR-$PROCESS_RUNNER_TEMP/preparation-bundle}" \
        --env "PROCESS_TEST_CALL_LOG=${PROCESS_TEST_CALL_LOG-}" \
        "$PROCESS_CONTAINER_ID" \
        /bin/bash "$PROCESS_SCRIPT" prepare
}

processing_container_file_mode() {
    local path=$1
    ensure_processing_container
    docker exec "$PROCESS_CONTAINER_ID" stat -c '%a' "$path"
}

prepopulate_verified_processing_release_cache() {
    local verified_archive="$TEST_TMP_ROOT/${TF_VERSION_BUMP_ARCHIVE_URL##*/}"
    if [[ ! -f "$verified_archive" ]]; then
        curl --fail --silent --show-error --location \
            --connect-timeout "$TF_VERSION_BUMP_PREFETCH_CONNECT_TIMEOUT_SECONDS" \
            --max-time "$TF_VERSION_BUMP_PREFETCH_MAX_TIME_SECONDS" \
            --output "$verified_archive" "$TF_VERSION_BUMP_ARCHIVE_URL"
    fi
    local actual_sha256
    actual_sha256=$(sha256_file "$verified_archive")
    [[ "$actual_sha256" == "$TF_VERSION_BUMP_ARCHIVE_SHA256" ]] \
        || fail "independently downloaded rc.9 fixture archive failed checksum verification"

    local cache_directory="$PROCESS_RUNNER_TEMP/tf-version-bump-release-cache"
    local cached_archive="$cache_directory/$TF_VERSION_BUMP_ARCHIVE_SHA256.tar.gz"
    mkdir -p "$cache_directory"
    cp "$verified_archive" "$cached_archive"
    actual_sha256=$(sha256_file "$cached_archive")
    [[ "$actual_sha256" == "$TF_VERSION_BUMP_ARCHIVE_SHA256" ]] \
        || fail "trusted processing fixture cache failed checksum verification"
}

write_controlled_update_report_archive() {
    local report_payload=$1
    local fixture_root="$PROCESS_RUNNER_TEMP/controlled-updater"
    local tool_directory="$fixture_root/tool"
    local archive="$fixture_root/tf-version-bump.tar.gz"
    mkdir -p "$tool_directory"
    cat >"$tool_directory/tf-version-bump" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "-version" ]]; then
    printf '%s\n' 'tf-version-bump 1.0.0-test-report'
    exit 0
fi

report_file=""
while [[ $# -gt 0 ]]; do
    if [[ "$1" == "-report-file" ]]; then
        report_file=$2
        break
    fi
    shift
done
[[ -n "$report_file" ]] || exit 64
[[ "$(<"${PROCESS_TEST_CALL_LOG:?}")" == "__MISSING__" ]] \
    || cp -- "$PROCESS_TEST_CALL_LOG" "$report_file"
EOF
    chmod 755 "$tool_directory/tf-version-bump"
    printf '%s' "$report_payload" >"$PROCESS_TMP_ROOT/update-report-payload"
    PROCESS_TEST_CALL_LOG="$PROCESS_TMP_ROOT/update-report-payload"
    tar -czf "$archive" -C "$tool_directory" tf-version-bump
    PROCESS_TF_VERSION_BUMP_VERSION="v1.0.0-test-report"
    PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256=$(sha256_file "$archive")
    local cache_directory="$PROCESS_RUNNER_TEMP/tf-version-bump-release-cache"
    mkdir -p "$cache_directory"
    cp -- "$archive" "$cache_directory/$PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256.tar.gz"
}

# Runs $4.. (redirected to $2/$3), asserts it succeeded, and asserts it emitted nothing on
# either stream.
assert_silent_success() {
    local description=$1 stdout_file=$2 stderr_file=$3
    shift 3
    if ! "$@" >"$stdout_file" 2>"$stderr_file"; then
        fail "$description failed: $(<"$stderr_file")"
    fi
    [[ ! -s "$stdout_file" && ! -s "$stderr_file" ]] \
        || fail "$description emitted unexpected output: stdout=$(<"$stdout_file") stderr=$(<"$stderr_file")"
}

assert_command_failure() {
    local run_command=$1 tmp_root=$2 stdout_failure_description=$3
    local expected_diagnostic=$4 description=$5
    local stdout_file="$tmp_root/failure.stdout"
    local stderr_file="$tmp_root/failure.stderr"

    if "$run_command" >"$stdout_file" 2>"$stderr_file"; then
        fail "$description succeeded"
    fi
    [[ ! -s "$stdout_file" ]] || fail "$description $stdout_failure_description"

    local diagnostic
    diagnostic=$(<"$stderr_file")
    [[ "$diagnostic" == *"$expected_diagnostic"* ]] \
        || fail "$description did not report '$expected_diagnostic': $diagnostic"
}

assert_processing_failure() {
    assert_command_failure run_processing_prepare "$PROCESS_TMP_ROOT" \
        "emitted unexpected output" "$1" "$2"
}

setup_discovery_repository() {
    cleanup_discovery_repository
    DISCOVERY_ALLOWED_PREFIXES=""
    DISCOVERY_MANUAL_PREFIX=""
    DISCOVERY_POLICY_ID="nonproduction"
    DISCOVERY_RUN_ID="1001"
    DISCOVERY_RUN_ATTEMPT="1"
    DISCOVERY_CALLER_REF="refs/heads/main"
    DISCOVERY_DEFAULT_BRANCH="main"

    DISCOVERY_TMP_ROOT=$(mktemp -d)
    DISCOVERY_REPO="$DISCOVERY_TMP_ROOT/work"
    DISCOVERY_REMOTE="$DISCOVERY_TMP_ROOT/origin.git"

    mkdir -m 700 "$DISCOVERY_TMP_ROOT/client-credentials"

    "$TEST_GIT" init --bare --initial-branch=main "$DISCOVERY_REMOTE" >/dev/null
    "$TEST_GIT" init --initial-branch=main "$DISCOVERY_REPO" >/dev/null
    fixture_commit "$DISCOVERY_REPO" "Discovery Test" "discovery-test@example.invalid" "test: create discovery fixture" --allow-empty
    DISCOVERY_CONTROL_OID=$("$TEST_GIT" -C "$DISCOVERY_REPO" rev-parse HEAD)
    "$TEST_GIT" -C "$DISCOVERY_REPO" remote add origin "$DISCOVERY_REMOTE"
    "$TEST_GIT" -C "$DISCOVERY_REPO" push --quiet --set-upstream origin main
}

add_discovery_branch() {
    local branch=$1
    "$TEST_GIT" -C "$DISCOVERY_REPO" push --quiet origin "HEAD:refs/heads/$branch"
}

create_discovery_commit() {
    local message=$1
    fixture_commit "$DISCOVERY_REPO" "Discovery Test" "discovery-test@example.invalid" "$message" --allow-empty
    "$TEST_GIT" -C "$DISCOVERY_REPO" rev-parse HEAD
}

add_numbered_discovery_branches() {
    local first=$1
    local last=$2
    local commands_file="$DISCOVERY_TMP_ROOT/update-refs"
    : >"$commands_file"

    local number
    for ((number = first; number <= last; number++)); do
        printf 'create refs/heads/state/limit/%03d %s\n' "$number" "$DISCOVERY_CONTROL_OID" >>"$commands_file"
    done
    "$TEST_GIT" --git-dir "$DISCOVERY_REMOTE" update-ref --stdin <"$commands_file"
}

run_discovery() {
    (
        cd "$DISCOVERY_REPO"
        DISCOVERY_ALLOWED_PREFIXES=${DISCOVERY_ALLOWED_PREFIXES-} \
            DISCOVERY_MANUAL_PREFIX=${DISCOVERY_MANUAL_PREFIX-} \
            DISCOVERY_POLICY_ID=${DISCOVERY_POLICY_ID-nonproduction} \
            DISCOVERY_RUN_ID=${DISCOVERY_RUN_ID-1001} \
            DISCOVERY_RUN_ATTEMPT=${DISCOVERY_RUN_ATTEMPT-1} \
            DISCOVERY_CONTROL_OID=${DISCOVERY_CONTROL_OID-} \
            DISCOVERY_CALLER_REF=${DISCOVERY_CALLER_REF-refs/heads/main} \
            DISCOVERY_DEFAULT_BRANCH=${DISCOVERY_DEFAULT_BRANCH-main} \
            RUNNER_TEMP=${RUNNER_TEMP-$DISCOVERY_TMP_ROOT/client-credentials} \
            CONTROL_CHECKOUT=${CONTROL_CHECKOUT-$DISCOVERY_REPO} \
            PATH=${DISCOVERY_PATH-$PATH} \
            "$DISCOVER_SCRIPT"
    )
}

test_discovery_resolves_origin_from_control_checkout() {
    # Production break caught: discovery runs from the workflow workspace but resolves origin from
    # its current directory rather than the checked-out control repository.
    setup_discovery_repository
    add_discovery_branch "state/nonproduction/example"
    "$TEST_GIT" init --bare --initial-branch=main "$DISCOVERY_TMP_ROOT/origin" >/dev/null
    "$TEST_GIT" -C "$DISCOVERY_REPO" push --quiet "$DISCOVERY_TMP_ROOT/origin" \
        HEAD:refs/heads/state/nonproduction/wrong

    local stdout_file="$DISCOVERY_TMP_ROOT/control-checkout.stdout"
    local stderr_file="$DISCOVERY_TMP_ROOT/control-checkout.stderr"
    if ! (
        cd "$DISCOVERY_TMP_ROOT"
        DISCOVERY_ALLOWED_PREFIXES="state/nonproduction/" \
            DISCOVERY_MANUAL_PREFIX="" \
            DISCOVERY_POLICY_ID="nonproduction" \
            DISCOVERY_RUN_ID="1001" \
            DISCOVERY_RUN_ATTEMPT="1" \
            DISCOVERY_CONTROL_OID="$DISCOVERY_CONTROL_OID" \
            DISCOVERY_CALLER_REF="refs/heads/main" \
            DISCOVERY_DEFAULT_BRANCH="main" \
            CONTROL_CHECKOUT="$DISCOVERY_REPO" \
            RUNNER_TEMP="$DISCOVERY_TMP_ROOT/client-credentials" \
            "$DISCOVER_SCRIPT"
    ) >"$stdout_file" 2>"$stderr_file"; then
        fail "discovery did not resolve origin from CONTROL_CHECKOUT: $(<"$stderr_file")"
    fi
    jq -e '.include | length == 1 and .[0].branch == "state/nonproduction/example"' \
        "$stdout_file" >/dev/null \
        || fail "control-checkout discovery did not produce the expected branch matrix"
}

test_discovery_uses_runner_git_not_a_workstation_shim() {
    # Production break caught: a workstation-style PATH entry (e.g. an asdf/direnv shim directory
    # placed ahead of the runner's own bin dirs) silently becomes the Git the shipped discovery
    # script runs. This proves both halves of that claim, not just that the script tolerates an
    # unreachable shim: with the poison shim as the ONLY Git reachable on PATH, discovery must
    # fail -- proving the script resolves `git` through an ordinary PATH search that reaches the
    # shim, not some absolute path or side channel that would bypass it. With the runner's real
    # Git placed ahead of the same shim on PATH, discovery must succeed -- proving PATH order,
    # not the shim's mere presence, is what decides which Git actually runs.
    setup_discovery_repository
    add_discovery_branch "state/nonproduction/example"
    DISCOVERY_ALLOWED_PREFIXES="state/nonproduction/"

    local real_git
    real_git=$(command -v "$TEST_GIT")
    local real_bash
    real_bash=$(command -v bash)
    local shim_toolchain="$DISCOVERY_TMP_ROOT/shim-toolchain"
    mkdir -m 700 "$shim_toolchain"
    local tool tool_path
    for tool in bash jq mktemp realpath rm sha256sum sort; do
        tool_path=$(command -v "$tool") || fail "missing required tool: $tool"
        ln -s "$tool_path" "$shim_toolchain/$tool"
    done
    cat >"$shim_toolchain/git" <<'EOF'
#!/usr/bin/env bash
exit 99
EOF
    chmod 700 "$shim_toolchain/git"

    # bash itself must resolve from the shim toolchain too, or the script's own
    # `#!/usr/bin/env bash` shebang fails before any git resolution is ever attempted, making
    # this half pass (with a vacuous exit 127) regardless of how discovery resolves `git`.
    if DISCOVERY_PATH="$shim_toolchain" run_discovery \
        >"$DISCOVERY_TMP_ROOT/shim-only.stdout" 2>"$DISCOVERY_TMP_ROOT/shim-only.stderr"; then
        fail "discovery succeeded with only the poison Git shim reachable on PATH"
    fi
    [[ ! -s "$DISCOVERY_TMP_ROOT/shim-only.stdout" ]] \
        || fail "shim-only discovery emitted JSON despite failing"
    [[ "$(<"$DISCOVERY_TMP_ROOT/shim-only.stderr")" \
        == "discovery input error: allowed prefix is not a valid literal branch prefix" ]] \
        || fail "shim-only discovery did not fail from the poison Git shim itself: $(<"$DISCOVERY_TMP_ROOT/shim-only.stderr")"

    # The runner's real Git and real bash are both placed ahead of the same shim toolchain, so
    # only PATH order -- not the shim's mere presence -- decides which of each actually runs.
    local output
    if ! output=$(DISCOVERY_PATH="$(dirname "$real_git"):$(dirname "$real_bash"):$shim_toolchain" \
        run_discovery 2>"$DISCOVERY_TMP_ROOT/runner-git.stderr"); then
        fail "discovery depended on the workstation-only Git shim: $(<"$DISCOVERY_TMP_ROOT/runner-git.stderr")"
    fi
    jq -e '.include | map(.branch) == ["state/nonproduction/example"]' <<<"$output" >/dev/null \
        || fail "ordinary runner Git did not discover the fixture branch: $output"
}

test_processing_exposes_only_prepare_and_validate() {
    # Production break caught: the credential-free processing stage still accepts Git transport
    # modes, instead of rejecting them before any transport input can be consumed.
    local mode status output
    output=$("$PROCESS_SCRIPT" --help)
    [[ "$output" == *"prepare"* && "$output" == *"validate"* \
        && "$output" != *"authenticated-"* ]] \
        || fail "processing help did not advertise only preparation and validation"

    for mode in authenticated-fetch authenticated-push-update authenticated-push-delete; do
        status=0
        "$PROCESS_SCRIPT" "$mode" >"$TEST_TMP_ROOT/$mode.stdout" \
            2>"$TEST_TMP_ROOT/$mode.stderr" || status=$?
        [[ "$status" == 2 ]] || fail "processing accepted removed mode: $mode"
        [[ "$(<"$TEST_TMP_ROOT/$mode.stderr")" == *"Usage:"* ]] \
            || fail "processing did not report usage for removed mode: $mode"
    done
}

assert_discovery_failure() {
    assert_command_failure run_discovery "$DISCOVERY_TMP_ROOT" \
        "emitted JSON" "$1" "$2"
}

test_actionlint_launcher_reports_pinned_version() {
    [[ -x "$ACTIONLINT" ]] || fail "actionlint launcher is not executable: $ACTIONLINT"

    local output
    output=$("$ACTIONLINT" -version)
    local reported_version
    reported_version=${output%%$'\n'*}
    [[ "$reported_version" == "v1.7.12" ]] || fail "actionlint launcher did not report version 1.7.12: $reported_version"
}

test_example_configs_pass_cli_dry_run() {
    # Production break caught: a copyable control config stops parsing or no longer drives the
    # published CLI against a direct Terraform root.
    local fixture_directory="$TEST_TMP_ROOT/config-cli"
    local fixture="$fixture_directory/main.tf"
    mkdir -p "$fixture_directory"
    cat >"$fixture" <<'EOF'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
EOF

    local config
    for config in "$NONPRODUCTION_CONFIG" "$PRODUCTION_CONFIG"; do
        local output
        output=$(cd "$REPOSITORY_ROOT" && go run . -pattern "$fixture" -config "$config" -dry-run)
        [[ "$output" == *"Would update provider 'aws' to version '~> 6.0'"* ]] \
            || fail "control config did not update the fixture provider: $config"
        [[ "$output" == *"Would update module source 'terraform-aws-modules/vpc/aws' to version '5.0.0'"* ]] \
            || fail "control config did not update the fixture module: $config"
    done
}

test_ci_uses_supported_go_and_runs_github_actions_poc_checks() {
    # Production break caught: repository CI drifts from the supported Go toolchain or stops
    # exercising the copyable workflow checks with their required Linux test infrastructure.
    yq -o=json '.jobs' "$REPOSITORY_ROOT/.github/workflows/ci.yml" | jq -e '
        .test["runs-on"] == "ubuntu-latest" and
        .test.strategy == null and
        any(.test.steps[];
            .name == "Set up Go" and
            .with["go-version-file"] == "go.mod" and
            .with["go-version"] == null) and
        all(.test.steps[];
            ((.if // "") | contains("matrix.go-version") | not)) and
        any(.test.steps[];
            .name == "Install GitHub Actions POC test prerequisites" and
            .run == "sudo apt-get update\nsudo apt-get install --yes bash curl git jq\n") and
        any(.test.steps[];
            .name == "Check Docker test infrastructure availability" and
            .run == "docker version") and
        any(.test.steps[];
            .name == "Run GitHub Actions POC checks" and
            .run == "make test-github-actions") and
        any(.build.steps[];
            .name == "Set up Go" and
            .with["go-version-file"] == "go.mod" and
            .with["go-version"] == null)
    ' >/dev/null || fail "repository CI does not source its complete Go pipeline from go.mod"
}

test_modules_require_supported_go_version() {
    # Production break caught: the main or provider-fixture module still advertises support for an
    # older Go toolchain than the one used to build and test the repository.
    local module_root
    for module_root in \
        "$REPOSITORY_ROOT" \
        "$SCRIPT_DIR/test-fixtures/test-provider"; do
        local go_version
        go_version=$(GOWORK=off go -C "$module_root" list -m -f '{{.GoVersion}}')
        [[ "$go_version" == "1.26" ]] \
            || fail "module does not require Go 1.26: $module_root ($go_version)"
    done
}

test_copyable_workflow_layout() {
    # Production break caught: the copyable example no longer installs the workflow, helpers, and
    # control configuration at the consumer's .github root.
    local consumer="$TEST_TMP_ROOT/operator-guide-consumer"
    mkdir -p "$consumer/.github"
    cp -R "$SCRIPT_DIR/.github/." "$consumer/.github/"
    [[ -f "$consumer/.github/workflows/tf-version-bump-reusable.yml" ]] \
        || fail "operator copy command did not install the reusable workflow"
    [[ -f "$consumer/.github/scripts/process-state-branch.sh" ]] \
        || fail "operator copy command did not install the processing helper"
    [[ -f "$consumer/.github/tf-version-bump/nonproduction.yml" ]] \
        || fail "operator copy command did not install the control config"
    [[ ! -e "$consumer/.github/.github" ]] \
        || fail "operator copy command nested the .github directory"
}

test_reusable_workflow_declares_lean_interface() {
    # Production break caught: a caller input, credential boundary, timeout, or action safety
    # setting drifts from the copyable reusable-workflow contract.
    [[ -f "$REUSABLE_WORKFLOW" ]] || fail "reusable workflow is missing: $REUSABLE_WORKFLOW"

    yq -o=json '.on.workflow_call.inputs' "$REUSABLE_WORKFLOW" | jq -e '
        keys == [
            "allowed_branch_prefixes", "automation_policy_id", "branch_prefix", "commit_author_email",
            "commit_author_name", "config_path", "dry_run", "max_parallel", "terraform_directories",
            "terraform_version", "tf_version_bump_archive_sha256",
            "tf_version_bump_version"
        ] and
        .automation_policy_id == {type: "string", required: true} and
        .allowed_branch_prefixes == {type: "string", required: true} and
        .branch_prefix == {type: "string", default: ""} and
        .config_path == {type: "string", required: true} and
        .terraform_directories == {type: "string", default: "."} and
        .terraform_version == {type: "string", required: true} and
        .tf_version_bump_version == {type: "string", required: true} and
        .tf_version_bump_archive_sha256 == {type: "string", required: true} and
        .dry_run == {type: "boolean", default: false} and
        .max_parallel == {type: "number", default: 4} and
        .commit_author_name == {type: "string", default: ""} and
        .commit_author_email == {type: "string", default: ""}
    ' >/dev/null || fail "reusable workflow inputs do not match the typed contract"

    yq -o=json '.on.workflow_call | has("secrets")' "$REUSABLE_WORKFLOW" | jq -e '. == false' \
        >/dev/null || fail "reusable workflow declares publication secrets"

    yq -o=json '.jobs' "$REUSABLE_WORKFLOW" | jq -e '
        keys == ["discover", "prepare", "publish", "validate", "verify"] and
        .discover["timeout-minutes"] == 10 and .discover.permissions == {contents: "read"} and
        .prepare["timeout-minutes"] == 30 and .prepare.permissions == {contents: "read"} and
        .validate["timeout-minutes"] == 30 and .validate.permissions == {contents: "read"} and
        .verify["timeout-minutes"] == 20 and .verify.permissions == {contents: "read"} and
        .publish["timeout-minutes"] == 15 and
        .publish.permissions == {contents: "write", issues: "write", "pull-requests": "write"} and
        (.publish | has("environment") | not) and
        ((.publish | tostring | contains("github_app")) | not) and
        ((.publish | tostring | contains("signing")) | not) and
        any(.publish.steps[]; .name == "Publish verified result" and
            .env.GH_TOKEN == "${{ github.token }}") and
        (. | tostring | contains("GIT_AUTH_TOKEN") | not) and
        ([.[] | .steps[]? | select((.uses // "") | startswith("hashicorp/setup-terraform@")) |
            .with.terraform_wrapper] | length > 0 and all(.[]; . == false))
    ' >/dev/null || fail "reusable workflow jobs do not preserve the lean safety contract"

    yq -o=json '.jobs' "$REUSABLE_WORKFLOW" | jq -e '
        [to_entries[] | .key as $job | .value.steps[]? |
         select((.uses // "") | startswith("actions/checkout@")) |
         {job: $job, path: .with.path, persist: .with["persist-credentials"]}] == [
            {job: "discover", path: "control", persist: true},
            {job: "prepare", path: "control", persist: false},
            {job: "prepare", path: "target", persist: false},
            {job: "validate", path: "control", persist: false},
            {job: "validate", path: "target", persist: false},
            {job: "verify", path: "control", persist: false},
            {job: "verify", path: "target", persist: false},
            {job: "publish", path: "control", persist: false},
            {job: "publish", path: "target", persist: true}
        ]
    ' >/dev/null || fail "reusable workflow checkout credential boundary is not exact"
}

test_reusable_workflow_wires_current_attempt_pipeline() {
    # Production break caught: a branch stage consumes another attempt's artefact, loses matrix
    # identity, or bypasses the helper mode that owns that stage.
    yq -o=json '.jobs' "$REUSABLE_WORKFLOW" | jq -e '
        .discover.outputs.matrix == "${{ steps.discover.outputs.matrix }}" and
        .prepare.needs == "discover" and
        .validate.needs == ["discover", "prepare"] and
        .validate.if == "${{ always() && needs.discover.result == '"'"'success'"'"' }}" and
        .verify.needs == ["discover", "prepare", "validate"] and
        .publish.needs == ["discover", "verify"] and
        .publish.if == "${{ always() && needs.discover.result == '"'"'success'"'"' }}" and
        all(.prepare, .validate, .verify, .publish;
            .strategy["fail-fast"] == false and
            .strategy["max-parallel"] == "${{ inputs.max_parallel }}" and
            .strategy.matrix == "${{ fromJSON(needs.discover.outputs.matrix) }}") and
        all(.prepare, .validate, .verify, .publish;
            any(.steps[]; .name == "Check current run attempt" and
                .env.EXPECTED_RUN_ATTEMPT == "${{ matrix.run_attempt }}" and
                .env.CURRENT_RUN_ATTEMPT == "${{ github.run_attempt }}")) and
        any(.prepare.steps[]; (.run // "") | contains("process-state-branch.sh\" prepare")) and
        any(.validate.steps[]; (.run // "") | contains("process-state-branch.sh\" validate")) and
        any(.verify.steps[]; (.run // "") | contains("reconcile-state-branch.sh\" verify")) and
        any(.publish.steps[]; (.run // "") | contains("reconcile-state-branch.sh\" publish")) and
        any(.prepare.steps[]; ((.uses // "") | startswith("actions/upload-artifact@")) and
            .with.name == "preparation-${{ matrix.run_id }}-${{ matrix.run_attempt }}-${{ matrix.automation_policy_id }}-${{ matrix.ref_hash }}") and
        any(.validate.steps[]; ((.uses // "") | startswith("actions/upload-artifact@")) and
            .with.name == "validation-${{ matrix.run_id }}-${{ matrix.run_attempt }}-${{ matrix.automation_policy_id }}-${{ matrix.ref_hash }}") and
        any(.verify.steps[]; ((.uses // "") | startswith("actions/upload-artifact@")) and
            .with.name == "verified-${{ matrix.run_id }}-${{ matrix.run_attempt }}-${{ matrix.automation_policy_id }}-${{ matrix.ref_hash }}")
    ' >/dev/null || fail "reusable workflow does not wire the current-attempt five-stage pipeline"
}

test_callers_define_weekly_policies_and_tool_pins() {
    # Production break caught: a copyable caller widens its branch policy, runs from a non-default
    # ref, loses manual dry-run propagation, or drifts from the tested toolchain.
    local workflow
    for workflow in "$NONPRODUCTION_WORKFLOW" "$PRODUCTION_WORKFLOW"; do
        [[ -f "$workflow" ]] || fail "caller workflow is missing: $workflow"
    done

    yq -o=json '.' "$NONPRODUCTION_WORKFLOW" | jq -e '
        .on.push.paths == [".github/tf-version-bump/nonproduction.yml"] and
        .on.schedule == [{cron: "17 4 * * 1", timezone: "Australia/Melbourne"}] and
        .jobs.automation.if == "${{ github.ref == format('"'"'refs/heads/{0}'"'"', github.event.repository.default_branch) }}" and
        .jobs.automation.uses == "./.github/workflows/tf-version-bump-reusable.yml" and
        .jobs.automation.with.allowed_branch_prefixes == "state/nonproduction/\nstate/staging/\naws-state/nonproduction/\naws-state/staging/\n" and
        .jobs.automation.with.branch_prefix == "${{ github.event_name == '"'"'workflow_dispatch'"'"' && inputs.branch_prefix || '"'"''"'"' }}" and
        .jobs.automation.with.dry_run == "${{ github.event_name == '"'"'workflow_dispatch'"'"' && inputs.dry_run || false }}" and
        .jobs.automation.with.config_path == ".github/tf-version-bump/nonproduction.yml"
    ' >/dev/null || fail "non-production caller policy is not the approved weekly policy"

    yq -o=json '.' "$PRODUCTION_WORKFLOW" | jq -e '
        .on.push.paths == [".github/tf-version-bump/production.yml"] and
        .on.schedule == [{cron: "43 4 * * 0", timezone: "Australia/Melbourne"}] and
        .jobs.automation.if == "${{ github.ref == format('"'"'refs/heads/{0}'"'"', github.event.repository.default_branch) }}" and
        .jobs.automation.with.allowed_branch_prefixes == "state/production/\naws-state/production/\n" and
        .jobs.automation.with.config_path == ".github/tf-version-bump/production.yml"
    ' >/dev/null || fail "production caller policy is not the approved weekly policy"

    for workflow in "$NONPRODUCTION_WORKFLOW" "$PRODUCTION_WORKFLOW"; do
        yq -o=json '.jobs.automation.with' "$workflow" | jq -e \
            --arg tf_version_bump_version "$TF_VERSION_BUMP_VERSION" \
            --arg tf_version_bump_archive_sha256 "$TF_VERSION_BUMP_ARCHIVE_SHA256" '
            .terraform_directories == "." and
            .terraform_version == "1.15.5" and
            .tf_version_bump_version == $tf_version_bump_version and
            .tf_version_bump_archive_sha256 == $tf_version_bump_archive_sha256 and
            (has("github_app_client_id") | not) and
            (has("unattended_checks_safe") | not) and
            (has("publication_environment") | not)
        ' >/dev/null || fail "caller does not pin the approved POC toolchain: $workflow"
    done

    for workflow in "$NONPRODUCTION_CONFIG" "$PRODUCTION_CONFIG"; do
        [[ -f "$workflow" ]] || fail "control config is missing: $workflow"
        yq -o=json '.' "$workflow" | jq -e '
            (has("pattern") | not) and
            (.providers | length) == 1 and (.modules | length) == 1
        ' >/dev/null || fail "control config does not contain one provider/module update: $workflow"
    done
}

test_config_validation_workflow_is_read_only() {
    # Production break caught: config pull requests gain credentials or state-branch execution,
    # stop validating both control files, or run an unverified updater release.
    [[ -f "$CONFIG_VALIDATION_WORKFLOW" ]] || fail "config validation workflow is missing: $CONFIG_VALIDATION_WORKFLOW"

    yq -o=json '.' "$CONFIG_VALIDATION_WORKFLOW" | jq -e '
        .on.pull_request.paths == [
            ".github/tf-version-bump/nonproduction.yml",
            ".github/tf-version-bump/production.yml"
        ] and
        .permissions == {contents: "read"} and
        (.jobs | keys) == ["validate-configs"] and
        .jobs["validate-configs"]["runs-on"] == "ubuntu-latest" and
        (.jobs["validate-configs"] | has("environment") | not) and
        ([.jobs["validate-configs"].steps[] | select((.uses // "") | startswith("actions/checkout@"))] == [{
            uses: "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
            with: {"persist-credentials": false}
        }]) and
        (. | tostring | contains("secrets.") | not) and
        (. | tostring | contains("discover-state-branches.sh") | not) and
        (. | tostring | contains("process-state-branch.sh") | not) and
        (. | tostring | contains("reconcile-state-branch.sh") | not) and
        (. | tostring | contains("terraform init") | not)
    ' >/dev/null || fail "config validation workflow does not preserve its read-only boundary"

    yq -o=json '.jobs["validate-configs"].steps' "$CONFIG_VALIDATION_WORKFLOW" | jq -e \
        --arg archive_url "$TF_VERSION_BUMP_ARCHIVE_URL" \
        --arg archive_sha256 "$TF_VERSION_BUMP_ARCHIVE_SHA256" \
        --arg release_version "${TF_VERSION_BUMP_VERSION#v}" '
        any(.[]; .name == "Download and verify tf-version-bump" and
            (.run | contains($archive_url)) and
            (.run | contains($archive_sha256)) and
            (.run | contains("sha256sum --check --status")) and
            (.run | contains("tar -xzf")) and
            ((.run | index("sha256sum --check --status")) < (.run | index("tar -xzf"))) and
            (.run | contains("tf-version-bump " + $release_version))) and
        any(.[]; .name == "Validate configuration dry runs" and
            (.run | contains("mktemp -d")) and
            (.run | contains(".github/tf-version-bump/nonproduction.yml")) and
            (.run | contains(".github/tf-version-bump/production.yml")) and
            (.run | contains("-pattern \"$fixture_directory/main.tf\" -config \"$config\" -dry-run")))
    ' >/dev/null || fail "config validation workflow does not verify and dry-run both example configs"
}

test_workflows_keep_representative_execution_boundaries() {
    # Production break caught: credentials reach a local Terraform stage, an action floats from its
    # reviewed commit, or setup consumes the branch deadline without its fixed step timeout.
    yq -o=json '.jobs' "$REUSABLE_WORKFLOW" | jq -e '
        ([.[] | .steps[]? | .uses // empty] | unique) == [
            "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
            "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
            "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
            "hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e"
        ] and
        .prepare.steps[0].name == "Record preparation deadline" and
        .validate.steps[0].name == "Record validation deadline" and
        all(.prepare.steps[], .validate.steps[];
            if .uses then .["timeout-minutes"] == 10 else true end) and
        ((.discover | tostring | contains("secrets.")) | not) and
        ((.prepare | tostring | contains("secrets.")) | not) and
        ((.validate | tostring | contains("secrets.")) | not) and
        ((.verify | tostring | contains("secrets.")) | not) and
        ((.publish | tostring | contains("secrets.")) | not) and
        ((.publish | tostring | contains("github_app")) | not) and
        ((.publish | tostring | contains("signing")) | not) and
        any(.publish.steps[]; .name == "Publish verified result" and
            .env.RECONCILE_DRY_RUN == "${{ inputs.dry_run }}" and
            .env.RECONCILE_COMMIT_AUTHOR_NAME == "${{ inputs.commit_author_name || '"'"'github-actions[bot]'"'"' }}" and
            .env.RECONCILE_COMMIT_AUTHOR_EMAIL == "${{ inputs.commit_author_email || '"'"'41898282+github-actions[bot]@users.noreply.github.com'"'"' }}")
    ' >/dev/null || fail "workflow execution boundaries or exact action pins drifted"

    local workflow
    for workflow in "$NONPRODUCTION_WORKFLOW" "$PRODUCTION_WORKFLOW"; do
        yq -o=json '.concurrency' "$workflow" | jq -e '
            .["cancel-in-progress"] == false and .queue == "max"
        ' >/dev/null || fail "caller does not queue overlapping policy runs: $workflow"
    done
}

test_runtime_helpers_provide_help() {
    local helper
    for helper in "$DISCOVER_SCRIPT" "$PROCESS_SCRIPT" "$RECONCILE_SCRIPT"; do
        [[ -x "$helper" ]] || fail "runtime helper is not executable: $helper"

        local output
        output=$("$helper" --help)
        [[ -n "$output" ]] || fail "runtime helper did not provide help: $helper"
    done
}

test_processing_help_documents_prepare_safety_contract() {
    # Production break caught: workflow authors cannot determine which checkout owns each
    # repository-relative input, how roots are delimited, where trusted data is allocated, or
    # whether successful safety-only preparation emits output.
    local output
    output=$("$PROCESS_SCRIPT" --help)

    [[ "$output" == *"prepare"* ]] \
        || fail "processing help did not document the prepare phase"
    [[ "$output" == *"PROCESS_CONTROL_CHECKOUT"* ]] \
        || fail "processing help did not document the control checkout"
    [[ "$output" == *"PROCESS_TARGET_CHECKOUT"* ]] \
        || fail "processing help did not document the target checkout"
    [[ "$output" == *"PROCESS_CONFIG_PATH"* && "$output" == *"repository-relative"* ]] \
        || fail "processing help did not document the relative config path"
    [[ "$output" == *"PROCESS_TERRAFORM_ROOTS"* && "$output" == *"newline-separated"* ]] \
        || fail "processing help did not document the Terraform roots format"
    [[ "$output" == *"RUNNER_TEMP"* && "$output" == *"TF_DATA_DIR"* ]] \
        || fail "processing help did not document trusted data allocation"
    [[ "$output" == *"no output"* ]] \
        || fail "processing help did not document the success output contract"
}

test_processing_rejects_absolute_config_path() {
    # Production break caught: an untrusted config path bypasses the immutable control-checkout
    # boundary and selects an arbitrary host file.
    setup_processing_workspace
    PROCESS_CONFIG_PATH="$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"

    assert_processing_failure \
        "processing path error: config path must be repository-relative" \
        "absolute config path"
}

test_processing_rejects_config_symlink_into_target_checkout() {
    # Production break caught: a relative config symlink resolves into the untrusted sibling
    # target checkout and is accepted because only its lexical path is checked.
    setup_processing_workspace
    ln -s "$PROCESS_TARGET_CHECKOUT/root/main.tf" \
        "$PROCESS_CONTROL_CHECKOUT/escaped-config.yml"
    PROCESS_CONFIG_PATH="escaped-config.yml"

    assert_processing_failure \
        "processing path error: config path resolves outside control checkout" \
        "config symlink into target checkout"
}

test_processing_rejects_missing_config_path() {
    # Production break caught: failed config canonicalisation falls back to the lexical path and
    # allows preparation to continue without the reviewed control-checkout configuration.
    setup_processing_workspace
    PROCESS_CONFIG_PATH=".github/tf-version-bump/missing.yml"

    assert_processing_failure \
        "processing path error: config path does not exist" \
        "missing config path"
}

test_processing_rejects_overlapping_control_and_target_checkouts() {
    # Production break caught: overlapping checkout roots let target-controlled content satisfy
    # the reviewed control-config boundary.
    setup_processing_workspace
    PROCESS_CONTROL_CHECKOUT="$PROCESS_TARGET_CHECKOUT"
    PROCESS_CONFIG_PATH="root/main.tf"

    assert_processing_failure \
        "processing path error: control and target checkouts must be distinct and non-overlapping" \
        "overlapping control and target checkouts"
}

test_processing_rejects_terraform_root_parent_traversal_into_control() {
    # Production break caught: a relative Terraform root traverses into the sibling immutable
    # control checkout and turns reviewed workflow files into an update target.
    setup_processing_workspace
    PROCESS_TERRAFORM_ROOTS="../control"

    assert_processing_failure \
        "processing path error: Terraform root must not contain '..'" \
        "Terraform root traversal into control checkout"
}

test_processing_rejects_missing_terraform_root() {
    # Production break caught: a missing configured root is silently omitted instead of failing
    # the branch before any update work begins.
    setup_processing_workspace
    PROCESS_TERRAFORM_ROOTS="missing"

    assert_processing_failure \
        "processing path error: Terraform root does not exist" \
        "missing Terraform root"
}

test_processing_rejects_terraform_root_symlink_outside_checkouts() {
    # Production break caught: a lexically relative root symlink resolves to a directory outside
    # both checkouts and redirects later update and Terraform writes there.
    setup_processing_workspace
    mkdir "$PROCESS_TMP_ROOT/outside-root"
    ln -s "$PROCESS_TMP_ROOT/outside-root" "$PROCESS_TARGET_CHECKOUT/escaped-root"
    PROCESS_TERRAFORM_ROOTS="escaped-root"

    assert_processing_failure \
        "processing path error: Terraform root resolves outside target checkout" \
        "Terraform root symlink outside checkouts"
}

test_processing_rejects_duplicate_canonical_terraform_roots() {
    # Production break caught: a symlink alias makes one canonical root run twice and gives its
    # changed files duplicate configured ownership.
    setup_processing_workspace
    ln -s "root" "$PROCESS_TARGET_CHECKOUT/root-alias"
    PROCESS_TERRAFORM_ROOTS=$'root\nroot-alias'

    assert_processing_failure \
        "processing path error: duplicate canonical Terraform root" \
        "duplicate canonical Terraform roots"
}

test_processing_rejects_terraform_file_symlink_into_control() {
    # Production break caught: a direct `.tf` symlink redirects the later updater from the target
    # root into a reviewed file in the sibling control checkout.
    setup_processing_workspace
    ln -s "$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml" \
        "$PROCESS_TARGET_CHECKOUT/root/escaped.tf"

    assert_processing_failure \
        "processing path error: Terraform file must be a regular non-symlink" \
        "Terraform file symlink into control checkout"
}

test_processing_rejects_lock_file_symlink_outside_checkouts() {
    # Production break caught: a lock-file symlink redirects Terraform's later lock write to a
    # file outside both checkout boundaries.
    setup_processing_workspace
    printf '%s\n' '# outside lock' >"$PROCESS_TMP_ROOT/outside.lock.hcl"
    ln -s "$PROCESS_TMP_ROOT/outside.lock.hcl" \
        "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl"

    assert_processing_failure \
        "processing path error: Terraform lock file must be a regular non-symlink" \
        "lock-file symlink outside checkouts"
}

test_processing_rejects_repository_terraform_directory() {
    # Production break caught: repository `.terraform` working data survives preflight and can
    # influence or redirect later Terraform initialisation.
    setup_processing_workspace
    mkdir "$PROCESS_TARGET_CHECKOUT/root/.terraform"

    assert_processing_failure \
        "processing path error: repository .terraform entry is forbidden" \
        "repository .terraform directory"
}

test_processing_rejects_runner_temp_inside_either_checkout() {
    # Production break caught: trusted Terraform data is allocated inside a checkout and becomes
    # writable or capturable through repository-controlled paths.
    setup_processing_workspace

    local checkout
    for checkout in "$PROCESS_CONTROL_CHECKOUT" "$PROCESS_TARGET_CHECKOUT"; do
        RUNNER_TEMP="$checkout"
        assert_processing_failure \
            "processing path error: RUNNER_TEMP must resolve outside both checkouts" \
            "RUNNER_TEMP inside checkout"
    done
}

test_processing_allocates_fresh_trusted_data_directory_per_nested_root() {
    # Production break caught: nested roots are rejected, share Terraform data, reuse a prior
    # invocation's directory, allocate inside a checkout, or emit unstable success output.
    setup_processing_workspace
    mkdir "$PROCESS_TARGET_CHECKOUT/root/child"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/root/child/main.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/child/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add nested processing root"
    PROCESS_TERRAFORM_ROOTS=$'root\nroot/child\n'

    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    local stub_body
    stub_body=$(cat <<'EOF'
printf '%s\t%s\n' "$TF_DATA_DIR" "$(stat -c '%a' "$TF_DATA_DIR")" \
    >>"${PROCESS_TEST_CALL_LOG:?}"
printf '%s\n' 'Terraform has been successfully initialized!'
EOF
    )
    write_terraform_stub "$fixture_bin/terraform" "$stub_body"
    PROCESS_PATH_PREFIX=$fixture_bin
    PROCESS_TEST_CALL_LOG="$PROCESS_TMP_ROOT/data-directories.log"

    local invocation
    for invocation in 1 2; do
        PROCESS_PREPARATION_BUNDLE_DIR="$PROCESS_RUNNER_TEMP/preparation-bundle-$invocation"
        if ! run_processing_prepare \
            >"$PROCESS_TMP_ROOT/prepare-$invocation.stdout" \
            2>"$PROCESS_TMP_ROOT/prepare-$invocation.stderr"; then
            local init_diagnostic=""
            if [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/logs/init-2.log" ]]; then
                init_diagnostic=$(<"$PROCESS_PREPARATION_BUNDLE_DIR/logs/init-2.log")
            fi
            fail "valid nested-root preparation failed: $(<"$PROCESS_TMP_ROOT/prepare-$invocation.stderr") $init_diagnostic"
        fi
        [[ ! -s "$PROCESS_TMP_ROOT/prepare-$invocation.stdout" \
            && ! -s "$PROCESS_TMP_ROOT/prepare-$invocation.stderr" ]] \
            || fail "valid nested-root preparation emitted unexpected output"
    done

    local -a data_directories=()
    local data_directory
    local permissions
    while IFS=$'\t' read -r data_directory permissions; do
        data_directories+=("$data_directory")
        [[ "$data_directory" == /* ]] \
            || fail "prepare did not allocate an absolute TF_DATA_DIR per root"
        [[ "$data_directory" != "$PROCESS_CONTROL_CHECKOUT"/* \
            && "$data_directory" != "$PROCESS_TARGET_CHECKOUT"/* ]] \
            || fail "prepare allocated TF_DATA_DIR inside a checkout"
        [[ "$permissions" == "700" ]] \
            || fail "prepare did not protect TF_DATA_DIR: $permissions"
        [[ ! -e "$data_directory" ]] \
            || fail "prepare did not clean a trusted TF_DATA_DIR after bundling"
    done <"$PROCESS_TEST_CALL_LOG"
    [[ "${#data_directories[@]}" -eq 4 ]] \
        || fail "prepare did not allocate one TF_DATA_DIR per root and invocation"
    local unique_count
    unique_count=$(printf '%s\n' "${data_directories[@]}" | LC_ALL=C sort -u | wc -l)
    unique_count=${unique_count//[[:space:]]/}
    [[ "$unique_count" -eq 4 ]] \
        || fail "prepare reused a trusted TF_DATA_DIR across roots or invocations"
}

test_processing_removes_incomplete_bundle_when_final_mode_change_fails() {
    # Production break caught: final publication moves the staged bundle before its root mode is
    # made read-only, then a chmod failure leaves that incomplete destination available to upload.
    setup_processing_workspace

    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    cat >"$fixture_bin/chmod" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -eq 2 && "$1" == "555" \
    && "$2" == "${PROCESS_PREPARATION_BUNDLE_DIR:?}" ]]; then
    echo "simulated final bundle chmod failure" >&2
    exit 1
fi
exec /bin/chmod "$@"
EOF
    chmod 755 "$fixture_bin/chmod"
    PROCESS_PATH_PREFIX=$fixture_bin

    local stdout_file="$PROCESS_TMP_ROOT/final-mode.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/final-mode.stderr"
    if run_processing_prepare >"$stdout_file" 2>"$stderr_file"; then
        fail "final bundle chmod failure unexpectedly succeeded"
    fi
    [[ ! -s "$stdout_file" ]] \
        || fail "final bundle chmod failure emitted unexpected stdout"
    grep -F "simulated final bundle chmod failure" "$stderr_file" >/dev/null \
        || fail "final bundle chmod failure did not reach finalisation"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR" ]] \
        || fail "final bundle chmod failure left an incomplete published bundle"
}

test_processing_rejects_unexpected_changed_path() {
    # Production break caught: an untracked file outside the direct Terraform/lock allow-list is
    # accepted into the future candidate patch and commit.
    setup_processing_workspace
    printf '%s\n' 'unexpected' >"$PROCESS_TARGET_CHECKOUT/notes.txt"

    assert_processing_failure \
        "processing status error: unexpected changed or untracked path" \
        "unexpected untracked path"
}

test_processing_rejects_newline_in_changed_path() {
    # Production break caught: a changed/untracked path containing an embedded newline was accepted
    # as a valid direct Terraform file. Left unrejected here, it would sail through prepare as a
    # "success" bundle and only fail much later in verify as an unexplained, unfiled red job,
    # instead of a bounded status error (and marked failure issue) at prepare.
    setup_processing_workspace
    local newline_named_file="$PROCESS_TARGET_CHECKOUT/root/bad"$'\n'"name.tf"
    printf '%s\n' 'terraform {}' >"$newline_named_file"

    assert_processing_failure \
        "processing status error: changed path must not contain a newline" \
        "newline-bearing changed path"
}

test_processing_rejects_non_utf8_changed_path() {
    # Candidate paths are stored in JSON, so preparation must reject bytes that cannot round-trip
    # through UTF-8 before publishing an ambiguous manifest.
    setup_processing_workspace
    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    local stub_body
    stub_body=$(cat <<'EOF'
invalid_path="${PROCESS_TARGET_CHECKOUT:?}/root/module"
invalid_path+=$'\377'
invalid_path+='.tf'
printf '%s\n' 'terraform {}' >"$invalid_path"
printf '%s\n' 'Terraform has been successfully initialized!'
EOF
    )
    write_terraform_stub "$fixture_bin/terraform" "$stub_body"
    PROCESS_PATH_PREFIX=$fixture_bin
    prepopulate_verified_processing_release_cache
    ensure_processing_container

    local host_target=$PROCESS_TARGET_CHECKOUT
    local container_target="/tmp/tf-version-bump-non-utf8-preparation-$RANDOM"
    local base_oid control_oid
    base_oid=$(processing_base_oid)
    control_oid=$(processing_control_oid)
    docker exec "$PROCESS_CONTAINER_ID" cp -a "$host_target" "$container_target"
    docker exec "$PROCESS_CONTAINER_ID" chown -R \
        "$(id -u):$(id -g)" "$container_target"

    PROCESS_TARGET_CHECKOUT="$container_target" \
        PROCESS_BASE_OID="$base_oid" \
        PROCESS_CONTROL_OID="$control_oid" \
        assert_processing_failure \
        "processing status error: changed path is not valid UTF-8" \
        "non-UTF-8 changed path"
}

test_reconciliation_rejects_non_utf8_candidate_path_collision() {
    # JSON replaces invalid UTF-8 bytes, so a raw patch path must not alias a distinct manifest
    # path containing the literal replacement character.
    setup_processing_workspace
    local declared_path=$'root/module\357\277\275.tf'
    printf '%s\n' 'terraform { required_version = ">= 1.15.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/$declared_path"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "$declared_path"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add manifest path collision fixture"

    ensure_processing_container
    local fixture_root="$PROCESS_TMP_ROOT/non-utf8-reconciliation"
    local bundle="$fixture_root/preparation"
    local outcome="$fixture_root/validation"
    local verified="$fixture_root/verified"
    local container_checkout="/tmp/tf-version-bump-non-utf8-checkout-$RANDOM"
    local container_candidate="/tmp/tf-version-bump-non-utf8-candidate-$RANDOM"
    mkdir -p "$bundle" "$outcome"
    docker exec "$PROCESS_CONTAINER_ID" cp -a "$PROCESS_TARGET_CHECKOUT" "$container_checkout"
    docker exec "$PROCESS_CONTAINER_ID" cp -a "$PROCESS_TARGET_CHECKOUT" "$container_candidate"
    docker exec "$PROCESS_CONTAINER_ID" chown -R \
        "$(id -u):$(id -g)" "$container_checkout" "$container_candidate"

    local fixture_script="$fixture_root/create-invalid-candidate.sh"
    cat >"$fixture_script" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
invalid_path="$CANDIDATE/root/module"
invalid_path+=$'\377'
invalid_path+='.tf'
printf '%s\n' 'terraform { required_version = ">= 1.15.0" }' >"$invalid_path"
git -C "$CANDIDATE" add -N -- "${invalid_path#"$CANDIDATE/"}"
git -C "$CANDIDATE" diff --binary --full-index --no-color >"$BUNDLE/update.patch"
EOF
    chmod 755 "$fixture_script"
    docker exec \
        --user "$(id -u):$(id -g)" \
        --env "CANDIDATE=$container_candidate" \
        --env "BUNDLE=$bundle" \
        "$PROCESS_CONTAINER_ID" /bin/bash "$fixture_script"

    local base_oid control_oid branch_hash patch_digest file_digest manifest_digest
    base_oid=$(processing_base_oid)
    control_oid=$(processing_control_oid)
    branch_hash=$(processing_ref_hash)
    patch_digest=$(sha256_file "$bundle/update.patch")
    file_digest=$(sha256_file "$PROCESS_TARGET_CHECKOUT/$declared_path")
    jq -n \
        --arg control_oid "$control_oid" \
        --arg base_oid "$base_oid" \
        --arg ref_hash "$branch_hash" \
        --arg state_branch "$PROCESS_STATE_BRANCH" \
        --arg changed_path "$declared_path" \
        --arg patch_digest "$patch_digest" \
        --arg file_digest "$file_digest" \
        '{schema_version: 2, run_id: "123456", run_attempt: "2",
          automation_policy_id: "nonproduction", control_oid: $control_oid,
          state_branch: $state_branch, base_oid: $base_oid, ref_hash: $ref_hash,
          classification: "success", roots: [{path: "root"}],
          updates: {module_blocks_updated: 0, provider_blocks_updated: 0,
                    changed_files: [{path: $changed_path, mode: "100644", sha256: $file_digest}],
                    patch_sha256: $patch_digest},
          formatting: {ran: false, changed_files: []},
          final_changed_files: [{path: $changed_path, mode: "100644", sha256: $file_digest}]}' \
        >"$bundle/manifest.json"
    manifest_digest=$(sha256_file "$bundle/manifest.json")
    jq -n \
        --arg control_oid "$control_oid" \
        --arg base_oid "$base_oid" \
        --arg ref_hash "$branch_hash" \
        --arg state_branch "$PROCESS_STATE_BRANCH" \
        --arg manifest_digest "$manifest_digest" \
        '{schema_version: 2, run_id: "123456", run_attempt: "2",
          automation_policy_id: "nonproduction", control_oid: $control_oid,
          state_branch: $state_branch, base_oid: $base_oid, ref_hash: $ref_hash,
          classification: "success", command_status: 0,
          candidate_manifest_sha256: $manifest_digest}' \
        >"$outcome/manifest.json"

    local stdout_file="$fixture_root/verify.stdout"
    local stderr_file="$fixture_root/verify.stderr"
    if docker exec \
        --user "$(id -u):$(id -g)" \
        --env GIT_CONFIG_COUNT=1 \
        --env GIT_CONFIG_KEY_0=safe.directory \
        --env "GIT_CONFIG_VALUE_0=$container_checkout" \
        --env RECONCILE_RUN_ID=123456 \
        --env RECONCILE_RUN_ATTEMPT=2 \
        --env RECONCILE_AUTOMATION_POLICY_ID=nonproduction \
        --env "RECONCILE_CONTROL_OID=$control_oid" \
        --env "RECONCILE_STATE_BRANCH=$PROCESS_STATE_BRANCH" \
        --env "RECONCILE_BASE_OID=$base_oid" \
        --env "RECONCILE_REF_HASH=$branch_hash" \
        --env "RECONCILE_PREPARATION_BUNDLE_DIR=$bundle" \
        --env "RECONCILE_VALIDATION_OUTCOME_DIR=$outcome" \
        --env "RECONCILE_TARGET_CHECKOUT=$container_checkout" \
        --env "RECONCILE_VERIFIED_RESULT_DIR=$verified" \
        "$PROCESS_CONTAINER_ID" /bin/bash "$RECONCILE_SCRIPT" verify \
        >"$stdout_file" 2>"$stderr_file"; then
        fail "verification accepted a non-UTF-8 candidate path collision"
    fi
    [[ ! -s "$stdout_file" ]] \
        || fail "non-UTF-8 candidate rejection emitted unexpected stdout"
    grep -F 'candidate patch path is not valid UTF-8' "$stderr_file" >/dev/null \
        || fail "non-UTF-8 candidate rejection emitted the wrong diagnostic"
    [[ ! -e "$verified" ]] \
        || fail "non-UTF-8 candidate rejection produced a verified result"
}

test_processing_rejects_gitignored_terraform_file() {
    # Production break caught: ordinary porcelain status omits an ignored direct `.tf` file, so
    # target-selected Terraform input bypasses the changed-path allow-list entirely.
    setup_processing_workspace
    printf '%s\n' 'root/ignored.tf' >"$PROCESS_TARGET_CHECKOUT/.gitignore"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- ".gitignore"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: ignore hostile Terraform input"
    printf '%s\n' 'terraform {}' >"$PROCESS_TARGET_CHECKOUT/root/ignored.tf"

    assert_processing_failure \
        "processing status error: ignored path is forbidden" \
        "gitignored Terraform file"
}

test_processing_rejects_deleted_terraform_file() {
    # Production break caught: a deleted direct `.tf` path is treated as an allowed source change
    # even though no regular file remains for the future candidate.
    setup_processing_workspace
    mv "$PROCESS_TARGET_CHECKOUT/root/main.tf" "$PROCESS_TMP_ROOT/deleted-main.tf"

    local status_output
    status_output=$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" \
        status --porcelain=v1 --untracked-files=all)
    [[ "$status_output" == " D root/main.tf" ]] \
        || fail "deletion fixture did not produce the expected real Git status: $status_output"

    assert_processing_failure \
        "processing status error: changed path must be a regular non-symlink file" \
        "deleted Terraform file"
}

test_processing_rejects_renamed_terraform_file() {
    # Production break caught: a staged rename between two otherwise allowed direct `.tf` paths
    # is accepted even though the old path is deleted from the future candidate.
    setup_processing_workspace
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" mv -- \
        "root/main.tf" "root/renamed.tf"

    local status_output
    status_output=$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" \
        status --porcelain=v1 --untracked-files=all)
    [[ "$status_output" == "R  root/main.tf -> root/renamed.tf" ]] \
        || fail "rename fixture did not produce the expected real Git status: $status_output"

    assert_processing_failure \
        "processing status error: renamed or copied paths are forbidden" \
        "renamed Terraform file"
}

test_processing_rejects_changed_terraform_file_not_directly_in_root() {
    # Production break caught: recursive containment attributes an omitted nested module's change
    # to its configured ancestor instead of requiring exactly one direct root.
    setup_processing_workspace
    mkdir "$PROCESS_TARGET_CHECKOUT/root/child"
    printf '%s\n' 'terraform {}' >"$PROCESS_TARGET_CHECKOUT/root/child/main.tf"
    PROCESS_TERRAFORM_ROOTS="root"

    assert_processing_failure \
        "processing status error: changed Terraform file must be directly within exactly one configured root" \
        "changed Terraform file below configured root"
}

test_processing_rejects_mismatched_control_head_before_target_write() {
    # Production break caught: caller-asserted control identity is copied into the manifest even
    # when the checked-out config and helper come from a different control revision.
    setup_processing_workspace
    PROCESS_CONTROL_OID="1111111111111111111111111111111111111111"

    assert_processing_failure \
        "processing setup error: control checkout HEAD does not match control OID" \
        "mismatched control checkout HEAD"
    grep -F 'required_version = ">= 1.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "mismatched control checkout changed target source"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR" ]] \
        || fail "mismatched control checkout produced a preparation bundle"
}

test_processing_rejects_mismatched_target_head_before_target_write() {
    # Production break caught: caller-asserted base identity is copied into the manifest even when
    # preparation reads and updates a different target revision.
    setup_processing_workspace
    PROCESS_BASE_OID="2222222222222222222222222222222222222222"

    assert_processing_failure \
        "processing setup error: target checkout HEAD does not match base OID" \
        "mismatched target checkout HEAD"
    grep -F 'required_version = ">= 1.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "mismatched target checkout changed target source"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR" ]] \
        || fail "mismatched target checkout produced a preparation bundle"
}

test_processing_rejects_mismatched_ref_hash_before_target_write() {
    # Production break caught: a caller-asserted branch hash can misname and misbind an artefact
    # because prepare never recomputes it from the complete state ref.
    setup_processing_workspace
    PROCESS_REF_HASH="3333333333333333333333333333333333333333333333333333333333333333"

    assert_processing_failure \
        "processing setup error: state ref hash does not match ref hash" \
        "mismatched complete-ref hash"
    grep -F 'required_version = ">= 1.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "mismatched complete-ref hash changed target source"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR" ]] \
        || fail "mismatched complete-ref hash produced a preparation bundle"
}

test_processing_prepares_with_released_cli_and_pinned_terraform() {
    # Production break caught: preparation substitutes a working-tree build, trusts an unchecked
    # archive, accepts the wrong tool version, or never applies the reviewed config to the direct
    # Terraform files before initialisation.
    setup_processing_workspace

    local stdout_file="$PROCESS_TMP_ROOT/preparation.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/preparation.stderr"
    assert_silent_success "released candidate preparation" \
        "$stdout_file" "$stderr_file" run_processing_prepare
    grep -F 'required_version = ">= 1.15.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "released CLI did not apply the reviewed Terraform version update"
}

test_processing_aggregates_released_cli_reports() {
    # Production break caught: preparation drops or mis-aggregates per-root block counts, or uses
    # a controlled executable instead of the published updater for the valid report contract.
    setup_processing_workspace
    cp -- "$NONPRODUCTION_CONFIG" \
        "$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
    mkdir "$PROCESS_TARGET_CHECKOUT/second"
    local terraform_root
    for terraform_root in root second; do
        cat >"$PROCESS_TARGET_CHECKOUT/$terraform_root/main.tf" <<'EOF'
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "4.0.0"
}
EOF
    done
    "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- \
        ".github/tf-version-bump/test.yml"
    fixture_commit "$PROCESS_CONTROL_CHECKOUT" "Processing Test" \
        "processing-test@example.invalid" "test: configure report aggregation"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- \
        "root/main.tf" "second/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" \
        "processing-test@example.invalid" "test: add report aggregation roots"

    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    local stub_body
    stub_body=$(cat <<'EOF'
root=${1#-chdir=}
printf '%s\n' '# controlled lock fixture' >"$root/.terraform.lock.hcl"
printf '%s\n' 'Terraform has been successfully initialized!'
EOF
    )
    write_terraform_stub "$fixture_bin/terraform" "$stub_body"
    PROCESS_PATH_PREFIX=$fixture_bin
    PROCESS_TERRAFORM_ROOTS=$'root\nsecond'
    prepopulate_verified_processing_release_cache

    local stdout_file="$PROCESS_TMP_ROOT/aggregation.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/aggregation.stderr"
    assert_silent_success "released multi-root report aggregation" \
        "$stdout_file" "$stderr_file" run_processing_prepare
    jq -e '
      .schema_version == 2 and
      .classification == "success" and
      .updates.module_blocks_updated == 2 and
      .updates.provider_blocks_updated == 2 and
      (.updates.changed_files | length) >= 2 and
      .formatting == {ran: false, changed_files: []} and
      .final_changed_files == .updates.changed_files
    ' "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
    [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]]
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/candidate.patch" ]]
}

test_processing_rejects_invalid_update_reports_as_automation() {
    # Production break caught: preparation accepts absent, malformed, non-v1, non-integral, or
    # extended updater reports and exposes their partial working-tree changes for publication.
    local row report_payload
    while IFS=$'\t' read -r row report_payload; do
        setup_processing_workspace
        write_controlled_update_report_archive "$report_payload"

        local stdout_file="$PROCESS_TMP_ROOT/$row.stdout"
        local stderr_file="$PROCESS_TMP_ROOT/$row.stderr"
        if run_processing_prepare >"$stdout_file" 2>"$stderr_file"; then
            fail "$row update report succeeded"
        fi
        [[ ! -s "$stdout_file" ]] \
            || fail "$row update report emitted unexpected stdout"
        jq -e '
          .schema_version == 2 and
          .classification == "automation" and
          .failure.stage == "tf-version-bump report" and
          has("updates") == false
        ' "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null
        [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]]
        [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/format.patch" ]]
    done <<'EOF'
missing	__MISSING__
malformed	not json
schema-2	{"schema_version":2,"module_blocks_updated":0,"provider_blocks_updated":0}
negative-count	{"schema_version":1,"module_blocks_updated":-1,"provider_blocks_updated":0}
fractional-count	{"schema_version":1,"module_blocks_updated":0,"provider_blocks_updated":1.5}
extra-key	{"schema_version":1,"module_blocks_updated":0,"provider_blocks_updated":0,"extra":true}
EOF
}

test_processing_rejects_release_archive_before_extraction_on_checksum_mismatch() {
    # Production break caught: an archive whose bytes do not match the recorded release digest is
    # extracted or executed before the mismatch is rejected.
    setup_processing_workspace
    PROCESS_TF_VERSION_BUMP_ARCHIVE_SHA256="0000000000000000000000000000000000000000000000000000000000000000"

    assert_processing_failure \
        "processing setup error: tf-version-bump release archive checksum mismatch" \
        "release archive checksum mismatch"
    [[ -z "$(find "$PROCESS_RUNNER_TEMP" -type f -name tf-version-bump -print -quit)" ]] \
        || fail "checksum-mismatched release archive was extracted before rejection"
    [[ -z "$(find "$PROCESS_RUNNER_TEMP" -mindepth 1 -maxdepth 1 \
        -type d \( -name 'tf-version-bump-data.*' -o -name 'preparation-bundle-stage.*' \) \
        -print -quit)" ]] \
        || fail "checksum failure did not clean private preparation staging"
}

test_processing_verifies_exact_terraform_version_before_target_writes() {
    # Production break caught: a mismatched setup-terraform binary is discovered only after the
    # updater has already changed target-controlled source files.
    setup_processing_workspace
    PROCESS_TERRAFORM_VERSION="1.15.4"

    assert_processing_failure \
        "processing setup error: Terraform reported an unexpected version" \
        "mismatched Terraform version"
    grep -F 'required_version = ">= 1.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "target source changed before Terraform version verification"
}

test_processing_rejects_expired_deadline_before_workspace_setup() {
    # Production break caught: prepare begins path/setup work and allocates trusted data after the
    # pre-recorded absolute branch budget has already expired.
    setup_processing_workspace
    PROCESS_PREPARATION_DEADLINE_EPOCH=$(($(date +%s) - 1))

    assert_processing_failure \
        "processing setup error: preparation deadline expired before workspace setup" \
        "expired preparation deadline"
    [[ -z "$(find "$PROCESS_RUNNER_TEMP" -mindepth 1 -maxdepth 1 \
        -type d -name 'tf-version-bump-data.*' -print -quit)" ]] \
        || fail "expired preparation deadline allocated trusted Terraform data"
}

test_processing_initialises_with_remaining_absolute_deadline() {
    # Production break caught: preparation renews its budget after setup, writes `.terraform`
    # beneath the checkout, skips initialisation, or omits the backend/input/colour safety flags.
    setup_processing_workspace
    cat >>"$PROCESS_TARGET_CHECKOUT/root/main.tf" <<'EOF'

terraform {
  backend "http" {
    address = "http://127.0.0.1:1/state"
  }
}
EOF
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add disabled backend fixture"
    PROCESS_PREPARATION_DEADLINE_EPOCH=$(($(date +%s) + 1200))

    local stdout_file="$PROCESS_TMP_ROOT/init.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/init.stderr"
    assert_silent_success "deadline-bound Terraform initialisation" \
        "$stdout_file" "$stderr_file" run_processing_prepare
    [[ ! -e "$PROCESS_TARGET_CHECKOUT/root/.terraform" ]] \
        || fail "Terraform initialisation wrote data inside the target checkout"

    local init_log
    init_log=$(find "$PROCESS_RUNNER_TEMP" -type f -name 'init-1.log' -print -quit)
    [[ -n "$init_log" ]] || fail "Terraform initialisation did not produce its captured log"
    grep -F 'Terraform has been successfully initialized!' "$init_log" >/dev/null \
        || fail "Terraform initialisation did not complete with backend disabled"
}

configure_processing_provider_root() {
    cat >"$PROCESS_TARGET_CHECKOUT/root/main.tf" <<'EOF'
terraform {
  required_version = ">= 1.0"

  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "3.2.4"
    }
  }
}
EOF
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add provider processing fixture"
}

test_processing_rejects_ignored_new_provider_lock() {
    # Production break caught: a provider package is installed but its newly generated lock file
    # is ignored, so the candidate cannot persist the provider selection reproducibly.
    setup_processing_workspace
    configure_processing_provider_root
    printf '%s\n' 'root/.terraform.lock.hcl' >"$PROCESS_TARGET_CHECKOUT/.gitignore"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- ".gitignore"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: ignore generated provider lock"

    assert_processing_failure \
        "processing status error: required provider lock file is ignored for Terraform root root" \
        "ignored newly required provider lock"
    [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" ]] \
        || fail "ignored required lock did not preserve an automation failure manifest"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]] \
        || fail "ignored required lock exposed a publishable patch"
    jq -e \
        '.classification == "automation" and .failure.root == "root"' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "ignored required lock was not classified as repository-policy automation failure"
    [[ -z "$(find "$PROCESS_RUNNER_TEMP" -mindepth 1 -maxdepth 1 \
        -type d -name 'tf-version-bump-data.*' -print -quit)" ]] \
        || fail "ignored required lock did not clean trusted Terraform data"
}

test_processing_init_timeout_preserves_failure_bundle_after_shared_budget() {
    # Production break caught: each root receives a fresh timeout, a later-root timeout loses its
    # deterministic root/stage attribution, or normal timeout handling exits before cleanup and
    # uploadable failure-bundle creation.
    setup_processing_workspace
    mkdir "$PROCESS_TARGET_CHECKOUT/second"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/second/main.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "second/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add second deadline root"

    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    local stub_body
    stub_body=$(cat <<'EOF'
root=${1#-chdir=}
printf '%s\n' "${root##*/}" >>"${PROCESS_TEST_CALL_LOG:?}"
if [[ "${root##*/}" == "root" ]]; then
    sleep 2
else
    trap '' TERM
    sleep 20
fi
printf '%s\n' 'Terraform has been successfully initialized!'
EOF
    )
    write_terraform_stub "$fixture_bin/terraform" "$stub_body" 1

    PROCESS_PATH_PREFIX=$fixture_bin
    PROCESS_TEST_CALL_LOG="$PROCESS_TMP_ROOT/terraform-calls.log"
    PROCESS_TERRAFORM_ROOTS=$'root\nsecond'
    prepopulate_verified_processing_release_cache
    ensure_processing_container
    local preparation_deadline_seconds=6
    PROCESS_PREPARATION_DEADLINE_EPOCH=$(($(date +%s) + preparation_deadline_seconds))

    local started_at
    started_at=$(date +%s)
    assert_processing_failure \
        "processing status error: terraform init timed out for Terraform root second" \
        "shared-budget second-root init timeout"
    local elapsed=$(( $(date +%s) - started_at ))
    # The ceiling is the deadline plus the script's own kill-after grace (--kill-after=1s) plus a
    # generous fixed allowance for scheduling/docker-exec overhead on a loaded runner. The property
    # under test is that one absolute budget is honoured and kill-after is applied -- not a tight
    # bound on wall-clock overhead.
    local kill_after_grace_seconds=1
    local overhead_allowance_seconds=5
    local elapsed_ceiling=$((preparation_deadline_seconds + kill_after_grace_seconds + overhead_allowance_seconds))
    [[ "$elapsed" -le "$elapsed_ceiling" ]] \
        || fail "non-returning init exceeded the one absolute preparation deadline plus its kill-after grace: ${elapsed}s (ceiling ${elapsed_ceiling}s)"
    [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" ]] \
        || fail "init timeout did not preserve an uploadable failure manifest"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]] \
        || fail "init timeout exposed a publishable candidate patch"
    jq -e \
        '.classification == "branch-init" and .failure.root == "second"' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "init timeout manifest did not attribute branch-init to the second root"
    [[ -z "$(find "$PROCESS_RUNNER_TEMP" -mindepth 1 -maxdepth 1 \
        -type d -name 'tf-version-bump-data.*' -print -quit)" ]] \
        || fail "init timeout did not clean trusted Terraform data"
    [[ "$(<"$PROCESS_TEST_CALL_LOG")" == $'root\nsecond' ]] \
        || fail "sequential roots were not attempted in configured order"
}

test_processing_timeout_terminates_the_supervised_process_tree() {
    # Production break caught: foreground timeout kills only the directly supervised shell, so a
    # target-controlled descendant can outlive preparation and act after cleanup.
    setup_processing_workspace
    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    local stub_body
    stub_body=$(cat <<'EOF'
trap '' TERM
(
    sleep 6
    printf '%s\n' 'descendant survived timeout' >"${PROCESS_TEST_CALL_LOG:?}.sentinel"
) &
printf '%s\n' "$!" >"${PROCESS_TEST_CALL_LOG:?}.pid"
wait
EOF
    )
    write_terraform_stub "$fixture_bin/terraform" "$stub_body"

    PROCESS_PATH_PREFIX=$fixture_bin
    PROCESS_TEST_CALL_LOG="$PROCESS_TMP_ROOT/process-tree"
    prepopulate_verified_processing_release_cache
    ensure_processing_container
    PROCESS_PREPARATION_DEADLINE_EPOCH=$(($(date +%s) + 4))

    assert_processing_failure \
        "processing status error: terraform init timed out for Terraform root root" \
        "process-tree init timeout"
    [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" \
        && ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]] \
        || fail "process-tree timeout did not preserve a non-publishable failure bundle"
    sleep 4
    [[ ! -e "$PROCESS_TEST_CALL_LOG.sentinel" ]] \
        || fail "supervised descendant survived timeout and wrote after cleanup"
}

# Shared by test_processing_aggregate_update_failure_has_no_publishable_patch and
# test_processing_failure_manifest_binds_candidate_lineage: a workspace where rc.9's own
# aggregate non-zero status (after it has already changed a valid sibling file) fails prepare.
create_aggregate_update_failure_fixture() {
    setup_processing_workspace
    printf '%s\n' 'terraform {' >"$PROCESS_TARGET_CHECKOUT/root/broken.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/broken.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add aggregate update failure"

    assert_processing_failure \
        "processing status error: tf-version-bump failed for Terraform root root" \
        "aggregate tf-version-bump failure"
}

test_processing_aggregate_update_failure_has_no_publishable_patch() {
    # Production break caught: rc.9's aggregate non-zero status is ignored after it changes a
    # valid sibling file, allowing a partial source update to become a publishable candidate.
    create_aggregate_update_failure_fixture
    grep -F 'required_version = ">= 1.15.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "aggregate failure fixture did not prove a later valid file was updated"
    [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" ]] \
        || fail "aggregate update failure did not preserve its failure manifest"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]] \
        || fail "aggregate update failure exposed a publishable candidate patch"
    jq -e \
        '.classification == "branch-update" and .failure.root == "root"' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "aggregate update failure was not classified for the affected root"
}

test_processing_failure_manifest_binds_candidate_lineage() {
    # Production break caught: a non-publishable failure bundle can cross run attempts, policies,
    # control/base revisions, or state refs because its upload identity is not manifest-bound.
    create_aggregate_update_failure_fixture
    local expected_control_oid
    local expected_base_oid
    local expected_ref_hash
    expected_control_oid=$(processing_control_oid)
    expected_base_oid=$(processing_base_oid)
    expected_ref_hash=$(processing_ref_hash)
    jq -e \
        --arg control_oid "$expected_control_oid" \
        --arg base_oid "$expected_base_oid" \
        --arg ref_hash "$expected_ref_hash" \
        '.schema_version == 2 and
         .run_id == "123456" and .run_attempt == "2" and
         .automation_policy_id == "nonproduction" and
         .control_oid == $control_oid and
         .state_branch == "state/nonproduction/example-thing" and
         .base_oid == $base_oid and
         .ref_hash == $ref_hash and
         .artifact_name == ("preparation-123456-2-nonproduction-" + $ref_hash)' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "failure manifest did not bind its immutable candidate lineage"
}

ensure_successful_preparation_bundle() {
    if [[ "$SUCCESSFUL_PREPARATION_READY" == "true" ]]; then
        return
    fi

    setup_processing_workspace
    configure_processing_provider_root
    mkdir "$PROCESS_TARGET_CHECKOUT/provider-free"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/provider-free/main.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "provider-free/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add provider-free preparation root"
    PROCESS_TERRAFORM_ROOTS=$'root\nprovider-free'

    local stdout_file="$PROCESS_TMP_ROOT/success.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/success.stderr"
    assert_silent_success "successful provider and provider-free preparation" \
        "$stdout_file" "$stderr_file" run_processing_prepare
    [[ -f "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" ]] \
        || fail "successful preparation did not produce a manifest"
    SUCCESSFUL_PREPARATION_READY=true
}

test_processing_success_manifest_binds_run_and_repository_identity() {
    # Production break caught: a candidate can be consumed under a different run attempt, policy,
    # control revision, state ref, base revision, or full-ref hash.
    ensure_successful_preparation_bundle
    local expected_control_oid
    local expected_base_oid
    local expected_ref_hash
    expected_control_oid=$(processing_control_oid)
    expected_base_oid=$(processing_base_oid)
    expected_ref_hash=$(processing_ref_hash)
    jq -e \
        --arg control_oid "$expected_control_oid" \
        --arg base_oid "$expected_base_oid" \
        --arg ref_hash "$expected_ref_hash" \
        '.run_id == "123456" and .run_attempt == "2" and
         .automation_policy_id == "nonproduction" and
         .control_oid == $control_oid and
         .state_branch == "state/nonproduction/example-thing" and
         .base_oid == $base_oid and
         .ref_hash == $ref_hash' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "successful manifest did not bind the immutable run/repository identity"
}

test_processing_success_manifest_binds_config_and_tool_pins() {
    # Production break caught: verification cannot reproduce the candidate because the reviewed
    # config or one of the exact updater/archive/Terraform pins is missing or ambiguous.
    ensure_successful_preparation_bundle
    jq -e \
        --arg tf_version_bump_version "$TF_VERSION_BUMP_VERSION" \
        --arg tf_version_bump_archive_sha256 "$TF_VERSION_BUMP_ARCHIVE_SHA256" \
        '.config_path == ".github/tf-version-bump/test.yml" and
         .tools.tf_version_bump.version == $tf_version_bump_version and
         .tools.tf_version_bump.archive_sha256 == $tf_version_bump_archive_sha256 and
         .tools.terraform.version == "1.15.5" and
         (.tools.terraform | has("image") | not)' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "successful manifest did not bind the config and exact tool pins"
}

test_processing_success_manifest_omits_provider_dependency_state() {
    # Production break caught: same-run provider-init details leak into the immutable candidate
    # contract, despite no later stage consuming them.
    ensure_successful_preparation_bundle
    [[ -f "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" \
        && ! -L "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" ]] \
        || fail "provider root did not retain a regular generated lock file"
    [[ ! -e "$PROCESS_TARGET_CHECKOUT/provider-free/.terraform.lock.hcl" ]] \
        || fail "provider-free root unexpectedly received a lock file"
    jq -e \
        '.roots == [{path: "root"}, {path: "provider-free"}]' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "successful manifest retained provider dependency state"
}

test_processing_success_bundle_has_immutable_binary_patch_and_artifact_key() {
    # Production break caught: the candidate has no replayable binary patch, its recorded digest
    # does not bind the bytes, or two run attempts/policies/full refs can collide on one artefact.
    ensure_successful_preparation_bundle
    local patch_file="$PROCESS_PREPARATION_BUNDLE_DIR/update.patch"
    [[ -f "$patch_file" && ! -L "$patch_file" ]] \
        || fail "successful preparation did not create a regular binary patch"
    local permissions
    permissions=$(processing_container_file_mode "$patch_file")
    [[ "$permissions" == "444" ]] \
        || fail "candidate patch was not made read-only: $permissions"
    local patch_sha256
    patch_sha256=$(sha256_file "$patch_file")
    local expected_ref_hash
    expected_ref_hash=$(processing_ref_hash)
    jq -e \
        --arg patch_sha256 "$patch_sha256" \
        --arg ref_hash "$expected_ref_hash" \
        '.artifact_name == ("preparation-123456-2-nonproduction-" + $ref_hash) and
         .updates.patch_sha256 == $patch_sha256' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "manifest did not bind the artifact key and exact patch bytes"

    local replay_checkout="$PROCESS_TMP_ROOT/replay"
    "$TEST_GIT" clone --quiet "$PROCESS_TARGET_CHECKOUT" "$replay_checkout"
    "$TEST_GIT" -C "$replay_checkout" apply --binary "$patch_file"
    grep -F 'required_version = ">= 1.15.0"' "$replay_checkout/root/main.tf" >/dev/null \
        || fail "binary patch did not replay the source update"
    [[ -f "$replay_checkout/root/.terraform.lock.hcl" ]] \
        || fail "binary patch did not replay the new provider lock"
}

test_processing_success_manifest_records_changed_paths_modes_and_hashes() {
    # Production break caught: verification cannot constrain the patch because a changed source
    # or lock path, Git mode, or exact post-update file digest is absent or wrong.
    ensure_successful_preparation_bundle
    local manifest="$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json"
    jq -e \
        '[.updates.changed_files[].path] == [
          "provider-free/main.tf",
          "root/.terraform.lock.hcl",
          "root/main.tf"
        ] and all(.updates.changed_files[]; .mode == "100644")' \
        "$manifest" >/dev/null \
        || fail "manifest did not record the exact sorted changed paths and Git modes"

    local relative_path
    local expected_sha256
    local recorded_sha256
    for relative_path in \
        "provider-free/main.tf" \
        "root/.terraform.lock.hcl" \
        "root/main.tf"; do
        expected_sha256=$(sha256_file "$PROCESS_TARGET_CHECKOUT/$relative_path")
        recorded_sha256=$(jq -er \
            --arg path "$relative_path" \
            '.updates.changed_files[] | select(.path == $path) | .sha256' \
            "$manifest")
        [[ "$recorded_sha256" == "$expected_sha256" ]] \
            || fail "manifest recorded the wrong SHA-256 for $relative_path"
    done
}

test_processing_success_manifest_records_correct_mode_for_glob_metacharacter_path() {
    # Production break caught: a changed filename containing pathspec glob metacharacters (e.g.
    # "a[5].tf") made a per-path `git ls-files --stage` mode lookup match a differently permissioned
    # sibling file instead of itself, corrupting the recorded mode -- which verification later
    # trusts as its executable-smuggling check.
    setup_processing_workspace
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/root/a[5].tf"
    printf '%s\n' 'resource "null_resource" "sibling" {}' \
        >"$PROCESS_TARGET_CHECKOUT/root/a5.tf"
    chmod 755 "$PROCESS_TARGET_CHECKOUT/root/a5.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/a[5].tf" "root/a5.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: add glob-metacharacter sibling fixture"

    local stdout_file="$PROCESS_TMP_ROOT/glob-metacharacter.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/glob-metacharacter.stderr"
    if ! run_processing_prepare >"$stdout_file" 2>"$stderr_file"; then
        fail "glob-metacharacter preparation failed: $(<"$stderr_file")"
    fi
    jq -e --arg path "root/a[5].tf" \
        '.updates.changed_files[] | select(.path == $path) | .mode == "100644"' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "manifest recorded the wrong mode for a glob-metacharacter changed path"
}

test_processing_no_change_runs_validation_and_skips_publication_mutation() {
    # Production break caught: an unchanged provider-free root emits an empty candidate patch,
    # skips validation, or reaches commit/ref/GitHub publication instead of completing neutrally.
    setup_processing_workspace
    printf '%s\n' 'terraform { required_version = ">= 1.15.0" }' \
        >"$PROCESS_TARGET_CHECKOUT/root/main.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- "root/main.tf"
    fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" "processing-test@example.invalid" "test: create unchanged provider-free root"

    run_processing_prepare
    jq -e '.classification == "no-change" and .updates.changed_files == [] and
        (.updates | has("patch_sha256") | not) and
        .formatting == {ran: false, changed_files: []} and .final_changed_files == []' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" >/dev/null \
        || fail "unchanged preparation did not produce a patch-free no-change manifest"
    [[ ! -e "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch" ]] \
        || fail "unchanged preparation emitted a candidate patch"

    run_processing_validate
    jq -e '.classification == "no-change"' \
        "$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json" >/dev/null \
        || fail "unchanged provider-free root did not complete validation"

    local verified="$PROCESS_RUNNER_TEMP/verified-no-change"
    RECONCILE_RUN_ID=123456 \
        RECONCILE_RUN_ATTEMPT=2 \
        RECONCILE_AUTOMATION_POLICY_ID=nonproduction \
        RECONCILE_CONTROL_OID="$(processing_control_oid)" \
        RECONCILE_STATE_BRANCH="$PROCESS_STATE_BRANCH" \
        RECONCILE_BASE_OID="$(processing_base_oid)" \
        RECONCILE_REF_HASH="$(processing_ref_hash)" \
        RECONCILE_PREPARATION_BUNDLE_DIR="$PROCESS_PREPARATION_BUNDLE_DIR" \
        RECONCILE_VALIDATION_OUTCOME_DIR="$PROCESS_VALIDATION_OUTCOME_DIR" \
        RECONCILE_TARGET_CHECKOUT="$PROCESS_TARGET_CHECKOUT" \
        RECONCILE_VERIFIED_RESULT_DIR="$verified" \
        "$RECONCILE_SCRIPT" verify
    jq -e '.classification == "no-change" and
        (.updates | has("patch_sha256") | not)' \
        "$verified/manifest.json" >/dev/null \
        || fail "verification did not bind the no-change result"

    local before_head before_refs before_status sentinel_bin
    before_head=$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" rev-parse HEAD)
    before_refs=$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" show-ref)
    before_status=$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" status --porcelain=v1)
    sentinel_bin="$PROCESS_RUNNER_TEMP/no-change-bin"
    mkdir "$sentinel_bin"
    cat >"$sentinel_bin/gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' called >"${NO_CHANGE_GH_SENTINEL:?}"
exit 99
EOF
    chmod 755 "$sentinel_bin/gh"

    PATH="$sentinel_bin:$PATH" \
        NO_CHANGE_GH_SENTINEL="$PROCESS_RUNNER_TEMP/gh-called" \
        RECONCILE_RUN_ID=123456 \
        RECONCILE_RUN_ATTEMPT=2 \
        RECONCILE_AUTOMATION_POLICY_ID=nonproduction \
        RECONCILE_CONTROL_OID="$(processing_control_oid)" \
        RECONCILE_STATE_BRANCH="$PROCESS_STATE_BRANCH" \
        RECONCILE_BASE_OID="$(processing_base_oid)" \
        RECONCILE_REF_HASH="$(processing_ref_hash)" \
        RECONCILE_VERIFIED_RESULT_DIR="$verified" \
        RECONCILE_TARGET_CHECKOUT="" \
        RECONCILE_GIT_REMOTE="" \
        RECONCILE_REPOSITORY="" \
        RECONCILE_COMMIT_AUTHOR_NAME="" \
        RECONCILE_COMMIT_AUTHOR_EMAIL="" \
        RECONCILE_DRY_RUN=false \
        GH_TOKEN="" \
        "$RECONCILE_SCRIPT" publish
    [[ "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" rev-parse HEAD)" == "$before_head" \
        && "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" show-ref)" == "$before_refs" \
        && "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" status --porcelain=v1)" == "$before_status" ]] \
        || fail "no-change publication mutated local Git state"
    [[ ! -e "$PROCESS_RUNNER_TEMP/gh-called" ]] \
        || fail "no-change publication called GitHub"

    yq -o=json '.jobs' "$REUSABLE_WORKFLOW" | jq -e '
        any(.prepare.steps[]; .name == "Confirm preparation classification" and
            (.run | contains("no-change"))) and
        any(.validate.steps[]; .name == "Validate candidate" and
            ((.run | contains("success")) and (.run | contains("no-change")))) and
        any(.validate.steps[]; .name == "Confirm validation classification" and
            (.run | contains("no-change")))
    ' >/dev/null || fail "workflow does not route no-change candidates through validation"
}

test_processing_validation_runs_provider_root() {
    # Production break caught: direct validation skips real provider execution, changes the
    # immutable bundle or lock, or fails to retain a successful command outcome.
    setup_processing_workspace
    configure_validation_provider_base
    create_validation_candidate_bundle
    local candidate_patch_sha256
    candidate_patch_sha256=$(sha256sum "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch")
    local candidate_manifest_sha256
    candidate_manifest_sha256=$(sha256sum "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json")
    local lock_sha256
    lock_sha256=$(sha256sum "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl")
    local stdout_file="$PROCESS_TMP_ROOT/provider-validation.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/provider-validation.stderr"
    GITHUB_TOKEN="must-not-enter-container" \
        assert_silent_success "real test-provider validation" \
        "$stdout_file" "$stderr_file" run_processing_validate
    grep -F 'required_version = ">= 1.15.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "verified candidate patch was not applied to the disposable checkout"

    local outcome="$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json"
    [[ -f "$outcome" ]] || fail "direct validation did not retain the validation outcome"
    jq -e \
        '.classification == "success" and .command_status == 0 and
         has("semantic_validation_authenticated") == false' "$outcome" >/dev/null \
        || fail "direct validation retained an unused authentication field"
    [[ "$(sha256sum "$PROCESS_PREPARATION_BUNDLE_DIR/update.patch")" == "$candidate_patch_sha256" \
        && "$(sha256sum "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json")" == "$candidate_manifest_sha256" ]] \
        || fail "provider execution modified the immutable candidate bundle"
    [[ "$(sha256sum "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl")" == "$lock_sha256" ]] \
        || fail "provider execution replaced the read-only disposable lock file"
}

test_processing_validation_accepts_update_patch_with_added_lock_file() {
    # Production break caught: applying the update patch only to the worktree leaves a newly added
    # provider lockfile untracked, so raw-diff verification omits it and rejects a valid manifest.
    setup_processing_workspace
    configure_validation_provider_base_without_lock
    create_validation_candidate_bundle true
    [[ ! -e "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" ]] \
        || fail "added-lock validation fixture already contained the candidate lockfile"

    local stdout_file="$PROCESS_TMP_ROOT/added-lock-validation.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/added-lock-validation.stderr"
    assert_silent_success "validation with added provider lockfile" \
        "$stdout_file" "$stderr_file" run_processing_validate
    [[ -f "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" ]] \
        || fail "validation did not apply the added provider lockfile"
    jq -e '.classification == "success" and .command_status == 0' \
        "$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json" >/dev/null \
        || fail "added-lock validation did not produce a successful outcome"
}

test_processing_validation_allows_provider_free_root_without_lock() {
    # Production break caught: a provider-free root without a lock is rejected. Terraform 1.15.5
    # tolerates readonly mode without a lock, so this test honestly proves acceptance, not omission.
    setup_processing_workspace
    create_validation_candidate_bundle

    if ! run_processing_validate \
        >"$PROCESS_TMP_ROOT/provider-free.stdout" \
        2>"$PROCESS_TMP_ROOT/provider-free.stderr"; then
        fail "provider-free validation failed: $(<"$PROCESS_TMP_ROOT/provider-free.stderr")"
    fi
    [[ ! -e "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" ]] \
        || fail "provider-free validation created a lock file in the read-only checkout"
    jq -e '.classification == "success" and .command_status == 0' \
        "$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json" >/dev/null \
        || fail "provider-free validation did not produce a successful host outcome"
}

test_processing_validation_runs_directly_without_docker_executable() {
    # Production break caught: validation requires Docker rather than using the installed pinned
    # Terraform CLI, or direct Terraform leaves undeclared files in the candidate checkout.
    setup_processing_workspace
    create_validation_candidate_bundle
    ensure_processing_container
    if docker exec "$PROCESS_CONTAINER_ID" command -v docker >/dev/null 2>&1; then
        fail "direct validation fixture unexpectedly provides Docker to the helper"
    fi

    local stdout_file="$PROCESS_TMP_ROOT/direct-validation.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/direct-validation.stderr"
    assert_silent_success "direct Terraform validation without Docker" \
        "$stdout_file" "$stderr_file" run_processing_validate
    [[ ! -e "$PROCESS_TARGET_CHECKOUT/root/.terraform" ]] \
        || fail "direct Terraform validation left a .terraform directory in the candidate checkout"
    [[ "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" diff HEAD --name-only)" == "root/main.tf" ]] \
        || fail "direct Terraform validation changed files outside the immutable candidate patch"
    jq -e '.classification == "success" and .command_status == 0' \
        "$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json" >/dev/null \
        || fail "direct Terraform validation did not produce a successful outcome"
}

test_processing_validation_rejects_installed_version_mismatch_before_candidate_apply() {
    # Production break caught: target content is applied before the installed Terraform CLI is
    # checked against the exact requested version.
    setup_processing_workspace
    create_validation_candidate_bundle
    chmod u+w "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json"
    jq '.tools.terraform.version = "1.15.4"' \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json" \
        >"$PROCESS_TMP_ROOT/mismatched-manifest.json"
    mv "$PROCESS_TMP_ROOT/mismatched-manifest.json" \
        "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json"
    chmod a-w "$PROCESS_PREPARATION_BUNDLE_DIR/manifest.json"
    PROCESS_TERRAFORM_VERSION="1.15.4"

    local stdout_file="$PROCESS_TMP_ROOT/version-mismatch.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/version-mismatch.stderr"
    if run_processing_validate >"$stdout_file" 2>"$stderr_file"; then
        fail "mismatched installed Terraform version succeeded"
    fi
    [[ ! -s "$stdout_file" ]] \
        || fail "mismatched installed Terraform version emitted unexpected stdout"
    grep -F 'Terraform reported an unexpected version' "$stderr_file" >/dev/null \
        || fail "mismatched installed Terraform version did not report the pin error"
    grep -F 'required_version = ">= 1.0"' \
        "$PROCESS_TARGET_CHECKOUT/root/main.tf" >/dev/null \
        || fail "candidate was applied before Terraform version verification"
}

test_processing_validation_produces_bounded_outcome_when_deadline_expires_before_first_log() {
    # Production break caught: a validation deadline that expires between passing its top-level
    # check and the first `terraform init` attempt left the validation data root with zero *.log
    # files. The unguarded `cp *.log` glob then crashed under `set -e` instead of the documented
    # bounded branch-validation outcome that verify/publish turn into a marked failure issue.
    #
    # The race is deterministically forced (rather than raced against the wall clock) with a fake
    # `date` that answers "not yet expired" for the two deadline checks that must succeed before
    # the root loop (the top-level dispatch check and verify_terraform_version's own check), then
    # "expired" from the third call onward -- exactly the root-1 init deadline check.
    setup_processing_workspace
    create_validation_candidate_bundle

    local fixture_bin="$PROCESS_TMP_ROOT/fixture-bin"
    mkdir "$fixture_bin"
    local clock_calls="$PROCESS_TMP_ROOT/fake-date-calls"
    cat >"$fixture_bin/date" <<EOF
#!/usr/bin/env bash
set -euo pipefail
count=0
[[ ! -f "$clock_calls" ]] || count=\$(<"$clock_calls")
count=\$((count + 1))
printf '%s\n' "\$count" >"$clock_calls"
if [[ "\$count" -le 2 ]]; then
    printf '%s\n' 1000000000
else
    printf '%s\n' 1000000100
fi
EOF
    chmod 755 "$fixture_bin/date"

    PROCESS_PATH_PREFIX=$fixture_bin
    PROCESS_VALIDATION_DEADLINE_EPOCH=1000000050

    local stdout_file="$PROCESS_TMP_ROOT/expired-deadline.stdout"
    local stderr_file="$PROCESS_TMP_ROOT/expired-deadline.stderr"
    if run_processing_validate >"$stdout_file" 2>"$stderr_file"; then
        fail "validation with a deadline expiring before the first log succeeded"
    fi
    [[ ! -s "$stdout_file" ]] \
        || fail "deadline-expiry validation emitted unexpected stdout: $(<"$stdout_file")"

    local outcome="$PROCESS_VALIDATION_OUTCOME_DIR/manifest.json"
    [[ -f "$outcome" ]] \
        || fail "deadline-expiry validation did not produce a bounded outcome manifest: $(<"$stderr_file")"
    jq -e '.classification == "branch-validation" and .failure.stage == "terraform init" and
           .failure.root == "root" and .command_status == 124' \
        "$outcome" >/dev/null \
        || fail "deadline-expiry outcome did not attribute a bounded timeout to terraform init"
    [[ -d "$PROCESS_VALIDATION_OUTCOME_DIR/logs" ]] \
        || fail "deadline-expiry outcome dropped the logs directory"
    [[ -z "$(find "$PROCESS_VALIDATION_OUTCOME_DIR/logs" -mindepth 1 -print -quit)" ]] \
        || fail "deadline-expiry outcome unexpectedly retained a log file that was never written"
}

test_processing_validation() {
    test_processing_validation_runs_directly_without_docker_executable
    test_processing_validation_runs_provider_root
    test_processing_validation_accepts_update_patch_with_added_lock_file
    test_processing_validation_allows_provider_free_root_without_lock
    test_processing_validation_rejects_installed_version_mismatch_before_candidate_apply
    test_processing_validation_produces_bounded_outcome_when_deadline_expires_before_first_log
}

test_processing_preparation() {
    test_processing_rejects_mismatched_control_head_before_target_write
    test_processing_rejects_mismatched_target_head_before_target_write
    test_processing_rejects_mismatched_ref_hash_before_target_write
    test_processing_prepares_with_released_cli_and_pinned_terraform
    test_processing_aggregates_released_cli_reports
    test_processing_rejects_invalid_update_reports_as_automation
    test_processing_rejects_release_archive_before_extraction_on_checksum_mismatch
    test_processing_verifies_exact_terraform_version_before_target_writes
    test_processing_rejects_expired_deadline_before_workspace_setup
    test_processing_initialises_with_remaining_absolute_deadline
    test_processing_rejects_ignored_new_provider_lock
    test_processing_init_timeout_preserves_failure_bundle_after_shared_budget
    test_processing_timeout_terminates_the_supervised_process_tree
    test_processing_aggregate_update_failure_has_no_publishable_patch
    test_processing_failure_manifest_binds_candidate_lineage
    test_processing_success_manifest_binds_run_and_repository_identity
    test_processing_success_manifest_binds_config_and_tool_pins
    test_processing_success_manifest_omits_provider_dependency_state
    test_processing_success_bundle_has_immutable_binary_patch_and_artifact_key
    test_processing_success_manifest_records_changed_paths_modes_and_hashes
    test_processing_success_manifest_records_correct_mode_for_glob_metacharacter_path
}

test_discovery_selects_literal_prefixes() {
    # Production break caught: glob-like matching, unsorted results, an inexact OID, or hashing the
    # short branch name instead of its complete ref can enter the immutable matrix.
    setup_discovery_repository
    add_discovery_branch "state/prod/zulu"
    local zulu_oid=$DISCOVERY_CONTROL_OID

    local alpha_oid
    alpha_oid=$(create_discovery_commit "test: create distinct alpha state")
    add_discovery_branch "state/prod/alpha"
    add_discovery_branch "state/staging/ignored"

    DISCOVERY_ALLOWED_PREFIXES="state/prod/"
    local output
    output=$(run_discovery)

    jq -e \
        --arg alpha_oid "$alpha_oid" \
        --arg zulu_oid "$zulu_oid" '
        [.include[] | {branch, base_oid, ref_hash}] == [
            {
                branch: "state/prod/alpha",
                base_oid: $alpha_oid,
                ref_hash: "b94bc8b90be3f93e0526dd9a3b482a65e404794cdc73685983c5e1f9db348f5a"
            },
            {
                branch: "state/prod/zulu",
                base_oid: $zulu_oid,
                ref_hash: "d1da02bddad6f20d7b9ce62309fe068a8d0a506aff2c1d1a957464ea1552baf3"
            }
        ]
    ' <<<"$output" >/dev/null || fail "discovery did not emit the expected sorted immutable entries: $output"
}

test_discovery_manual_prefix_only_narrows() {
    # Production break caught: a manual dispatch prefix can widen its caller's configured policy or
    # is ignored instead of selecting only its matching subset.
    setup_discovery_repository
    add_discovery_branch "state/nonproduction/example-one"
    add_discovery_branch "state/nonproduction/other"
    add_discovery_branch "state/production/example-one"

    DISCOVERY_ALLOWED_PREFIXES="state/nonproduction/"
    DISCOVERY_MANUAL_PREFIX="state/nonproduction/example-"
    local output
    output=$(run_discovery)
    jq -e '.include | map(.branch) == ["state/nonproduction/example-one"]' <<<"$output" >/dev/null \
        || fail "manual discovery prefix did not narrow the selected branches: $output"

    DISCOVERY_MANUAL_PREFIX="state/production/"
    local failure_stdout="$DISCOVERY_TMP_ROOT/manual-widen.stdout"
    if run_discovery >"$failure_stdout" 2>"$DISCOVERY_TMP_ROOT/manual-widen.stderr"; then
        fail "manual discovery prefix widened the configured allow-list"
    fi
    [[ ! -s "$failure_stdout" ]] || fail "widening manual prefix emitted a matrix"
}

test_discovery_deduplicates_overlapping_prefixes() {
    # Production break caught: one remote branch matching two allowed prefixes creates duplicate
    # matrix jobs for the same immutable ref.
    setup_discovery_repository
    add_discovery_branch "state/nonproduction/example"

    DISCOVERY_ALLOWED_PREFIXES=$'state/\nstate/nonproduction/'
    local output
    output=$(run_discovery)

    jq -e '.include | map(.branch) == ["state/nonproduction/example"]' <<<"$output" >/dev/null \
        || fail "overlapping prefixes produced duplicate discovery entries: $output"
}

test_discovery_accepts_caller_block_scalar_prefixes() {
    # Production break caught: the caller's newline-terminated YAML block scalar gains an empty
    # prefix during shell parsing and makes every workflow run fail before branch discovery.
    setup_discovery_repository
    add_discovery_branch "state/nonproduction/example"
    add_discovery_branch "state/staging/example"

    DISCOVERY_ALLOWED_PREFIXES=$'state/nonproduction/\nstate/staging/\naws-state/nonproduction/\naws-state/staging/\n'
    local output
    output=$(run_discovery)

    jq -e '.include | map(.branch) == [
        "state/nonproduction/example",
        "state/staging/example"
    ]' <<<"$output" >/dev/null \
        || fail "caller-shaped prefixes did not produce the expected branch matrix: $output"
}

test_discovery_rejects_invalid_inputs_by_stage() {
    # Production break caught: invalid policy/caller inputs reach matrix creation, failures emit
    # partial JSON, or diagnostics attribute the failure to the wrong discovery stage.
    setup_discovery_repository
    add_discovery_branch "state/prod/example"

    DISCOVERY_MANUAL_PREFIX=""
    DISCOVERY_CALLER_REF="refs/heads/main"
    DISCOVERY_ALLOWED_PREFIXES=""
    assert_discovery_failure "discovery input error:" "empty allow-list"

    DISCOVERY_ALLOWED_PREFIXES=$'state/\n\nstate/prod/'
    assert_discovery_failure "discovery input error:" "allow-list with an empty entry"

    DISCOVERY_ALLOWED_PREFIXES="state//prod/"
    assert_discovery_failure "discovery input error:" "malformed branch prefix"

    DISCOVERY_ALLOWED_PREFIXES=$'state/\tprod/'
    assert_discovery_failure "discovery input error:" "branch prefix containing a control character"

    DISCOVERY_ALLOWED_PREFIXES="/state/prod/"
    assert_discovery_failure "discovery input error:" "absolute-looking branch prefix"

    DISCOVERY_ALLOWED_PREFIXES="state/missing/"
    assert_discovery_failure "discovery selection error:" "allow-list with no remote matches"

    DISCOVERY_ALLOWED_PREFIXES="state/prod/"
    DISCOVERY_CALLER_REF="refs/heads/feature"
    assert_discovery_failure "discovery caller error:" "non-default caller ref"
}

test_discovery_treats_hostile_refs_as_inert_data() {
    # Production break caught: shell evaluation, word splitting, option parsing, locale filtering,
    # or JSON interpolation executes or corrupts a Git-valid remote branch name.
    setup_discovery_repository

    # shellcheck disable=SC2016 # The hostile ref must remain literal data.
    local command_substitution='state/$(touch${IFS}discovery-command-executed)'
    local quoted='state/quote"branch'
    local semicolon='state/semi;colon'
    local leading_dash='-leading-dash'
    local unicode='state/café'
    local percent='state/100%'
    # shellcheck disable=SC2016 # GitHub's injection example must remain literal data.
    local github_injection='state/zzz";echo${IFS}"hello";#'
    add_discovery_branch "$command_substitution"
    add_discovery_branch "$quoted"
    add_discovery_branch "$semicolon"
    add_discovery_branch "$leading_dash"
    add_discovery_branch "$unicode"
    add_discovery_branch "$percent"
    add_discovery_branch "$github_injection"

    DISCOVERY_ALLOWED_PREFIXES=$'-\nstate/'
    local output
    output=$(run_discovery)

    local actual
    actual=$(jq -c '[.include[].branch]' <<<"$output")
    local expected
    expected=$(jq -cn \
        --arg leading_dash "$leading_dash" \
        --arg command_substitution "$command_substitution" \
        --arg percent "$percent" \
        --arg unicode "$unicode" \
        --arg quoted "$quoted" \
        --arg semicolon "$semicolon" \
        --arg github_injection "$github_injection" \
        '[
            $leading_dash,
            $command_substitution,
            $percent,
            $unicode,
            $quoted,
            $semicolon,
            $github_injection
        ]')
    [[ "$actual" == "$expected" ]] || fail "hostile refs were executed, filtered, or corrupted: $output"
    [[ ! -e "$DISCOVERY_REPO/discovery-command-executed" ]] \
        || fail "hostile branch command substitution was executed"
}

test_discovery_enforces_matrix_limit_before_output() {
    # Production break caught: discovery sends a 257-job matrix to GitHub instead of stopping
    # before JSON emission with actionable policy-narrowing guidance.
    setup_discovery_repository
    add_numbered_discovery_branches 0 255
    DISCOVERY_ALLOWED_PREFIXES="state/limit/"

    local output
    output=$(run_discovery)
    jq -e '.include | length == 256' <<<"$output" >/dev/null \
        || fail "discovery rejected or truncated exactly 256 branches: $output"

    add_numbered_discovery_branches 256 256
    local stdout_file="$DISCOVERY_TMP_ROOT/limit.stdout"
    local stderr_file="$DISCOVERY_TMP_ROOT/limit.stderr"
    if run_discovery >"$stdout_file" 2>"$stderr_file"; then
        fail "discovery accepted 257 matrix entries"
    fi
    [[ ! -s "$stdout_file" ]] || fail "257-entry discovery emitted matrix JSON"
    local diagnostic
    diagnostic=$(<"$stderr_file")
    [[ "$diagnostic" == *"discovery matrix error:"* ]] \
        || fail "257-entry discovery did not identify the matrix stage: $diagnostic"
    [[ "$diagnostic" == *"narrow or partition"* ]] \
        || fail "257-entry discovery did not instruct the caller to narrow or partition: $diagnostic"
}

test_discovery_binds_supplied_immutable_identities() {
    # Production break caught: matrix entries omit run lineage/policy identity or re-resolve a
    # moved default branch instead of retaining the one caller-supplied control OID.
    setup_discovery_repository
    local original_control_oid=$DISCOVERY_CONTROL_OID
    add_discovery_branch "state/prod/example"

    local moved_default_oid
    moved_default_oid=$(create_discovery_commit "test: move default branch before discovery")
    [[ "$moved_default_oid" != "$original_control_oid" ]] || fail "default branch fixture did not move"
    "$TEST_GIT" -C "$DISCOVERY_REPO" push --quiet origin "HEAD:refs/heads/main"

    DISCOVERY_ALLOWED_PREFIXES="state/prod/"
    DISCOVERY_RUN_ID="8123456789"
    DISCOVERY_RUN_ATTEMPT="3"
    DISCOVERY_POLICY_ID="production"
    DISCOVERY_CONTROL_OID="$original_control_oid"
    local output
    output=$(run_discovery)

    jq -e \
        --arg run_id "$DISCOVERY_RUN_ID" \
        --arg run_attempt "$DISCOVERY_RUN_ATTEMPT" \
        --arg policy_id "$DISCOVERY_POLICY_ID" \
        --arg control_oid "$original_control_oid" '
        ((.include | length) == 1
            and all(.include[];
                .run_id == $run_id
                    and .run_attempt == $run_attempt
                    and .automation_policy_id == $policy_id
                    and .control_oid == $control_oid))
            and (.include[0] | keys) == [
                "automation_policy_id",
                "base_oid",
                "branch",
                "control_oid",
                "ref_hash",
                "run_attempt",
                "run_id"
            ]
    ' <<<"$output" >/dev/null || fail "discovery did not bind supplied immutable identities: $output"
}

test_discovery_validates_policy_id() {
    # Production break caught: an empty, syntactically unsafe, uppercase, or overlength policy ID
    # reaches matrix entries, or a valid boundary value is rejected.
    setup_discovery_repository
    add_discovery_branch "state/prod/example"
    DISCOVERY_ALLOWED_PREFIXES="state/prod/"

    local valid_policy_id
    for valid_policy_id in "a" "a-------------------------------"; do
        DISCOVERY_POLICY_ID=$valid_policy_id
        local output
        output=$(run_discovery)
        jq -e --arg policy_id "$valid_policy_id" \
            '.include | length == 1 and .[0].automation_policy_id == $policy_id' <<<"$output" >/dev/null \
            || fail "valid policy ID was rejected or corrupted: $valid_policy_id"
    done

    local invalid_policy_id
    for invalid_policy_id in \
        "" \
        "Production" \
        "-production" \
        "non_production" \
        "a--------------------------------"; do
        DISCOVERY_POLICY_ID=$invalid_policy_id
        assert_discovery_failure "discovery input error:" "invalid policy ID '$invalid_policy_id'"
    done
}

test_processing_path_and_workspace_safety() {
    test_processing_help_documents_prepare_safety_contract
    test_processing_rejects_absolute_config_path
    test_processing_rejects_config_symlink_into_target_checkout
    test_processing_rejects_missing_config_path
    test_processing_rejects_overlapping_control_and_target_checkouts
    test_processing_rejects_terraform_root_parent_traversal_into_control
    test_processing_rejects_missing_terraform_root
    test_processing_rejects_terraform_root_symlink_outside_checkouts
    test_processing_rejects_duplicate_canonical_terraform_roots
    test_processing_rejects_terraform_file_symlink_into_control
    test_processing_rejects_lock_file_symlink_outside_checkouts
    test_processing_rejects_repository_terraform_directory
    test_processing_rejects_runner_temp_inside_either_checkout
    test_processing_allocates_fresh_trusted_data_directory_per_nested_root
    test_processing_removes_incomplete_bundle_when_final_mode_change_fails
    test_processing_rejects_unexpected_changed_path
    test_processing_rejects_newline_in_changed_path
    test_processing_rejects_non_utf8_changed_path
    test_reconciliation_rejects_non_utf8_candidate_path_collision
    test_processing_rejects_gitignored_terraform_file
    test_processing_rejects_deleted_terraform_file
    test_processing_rejects_renamed_terraform_file
    test_processing_rejects_changed_terraform_file_not_directly_in_root
}

cleanup_test_repositories() {
    cleanup_discovery_repository
    cleanup_processing_workspace
    cleanup_processing_container
    rm -rf -- "$TEST_TMP_ROOT"
}

trap cleanup_test_repositories EXIT

if [[ $# -eq 0 ]]; then
    test_actionlint_launcher_reports_pinned_version
    test_example_configs_pass_cli_dry_run
    test_ci_uses_supported_go_and_runs_github_actions_poc_checks
    test_modules_require_supported_go_version
    test_copyable_workflow_layout
    test_reusable_workflow_declares_lean_interface
    test_reusable_workflow_wires_current_attempt_pipeline
    test_callers_define_weekly_policies_and_tool_pins
    test_config_validation_workflow_is_read_only
    test_workflows_keep_representative_execution_boundaries
    test_runtime_helpers_provide_help
    TEST_GIT="$TEST_GIT" "$RECONCILE_TEST"
    test_processing_path_and_workspace_safety
    test_processing_preparation
    test_processing_no_change_runs_validation_and_skips_publication_mutation
    test_processing_validation
    test_discovery_resolves_origin_from_control_checkout
    test_discovery_uses_runner_git_not_a_workstation_shim
    test_processing_exposes_only_prepare_and_validate
    test_discovery_selects_literal_prefixes
    test_discovery_manual_prefix_only_narrows
    test_discovery_deduplicates_overlapping_prefixes
    test_discovery_accepts_caller_block_scalar_prefixes
    test_discovery_rejects_invalid_inputs_by_stage
    test_discovery_treats_hostile_refs_as_inert_data
    test_discovery_enforces_matrix_limit_before_output
    test_discovery_binds_supplied_immutable_identities
    test_discovery_validates_policy_id
else
    "$@"
fi
