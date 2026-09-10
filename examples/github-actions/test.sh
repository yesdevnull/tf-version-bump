#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
DISCOVER_SCRIPT="$SCRIPT_DIR/.github/scripts/discover-state-branches.sh"
PROCESS_SCRIPT="$SCRIPT_DIR/.github/scripts/process-state-branch.sh"
RECONCILE_TEST="$SCRIPT_DIR/reconcile-test.sh"
REUSABLE_WORKFLOW="$SCRIPT_DIR/.github/workflows/tf-version-bump-reusable.yml"
TEST_GIT=${TEST_GIT-git}

DISCOVERY_TMP_ROOT=""
DISCOVERY_REPO=""
DISCOVERY_REMOTE=""
DISCOVERY_CONTROL_OID=""

PROCESS_TMP_ROOT=""
PROCESS_CONTROL_CHECKOUT=""
PROCESS_TARGET_CHECKOUT=""
PROCESS_RUNNER_TEMP=""
PROCESS_RESULT_DIR=""
PROCESS_VALIDATION_FIXTURE_ROOT=""
PROCESS_CONTAINER_ID=""
PROCESS_PATH_PREFIX=""
PROCESS_TEST_CALL_LOG=""

TEST_TMP_ROOT=$(mktemp -d)
TEST_TMP_ROOT=$(realpath "$TEST_TMP_ROOT")

TF_VERSION_BUMP_VERSION="v1.0.0-rc.11"
TF_VERSION_BUMP_ARCHIVE_SHA256="5560b45e220650e8b18d5836eff05d471f602a6ac970aeeb9628781797f54c85"
# Release-pin tooling maintains this explicit URL alongside the runtime digest.
# shellcheck disable=SC2034
TF_VERSION_BUMP_ARCHIVE_URL="https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0-rc.11/tf-version-bump_1.0.0-rc.11_linux_x86_64.tar.gz"
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
    local startup_log="$TEST_TMP_ROOT/container-start.log"
    if ! PROCESS_CONTAINER_ID=$(docker run --detach --pull="${1:-missing}" \
        --platform linux/amd64 \
        --entrypoint /bin/sh \
        --volume "$SCRIPT_DIR:$SCRIPT_DIR:ro" \
        --volume "$TEST_TMP_ROOT:$TEST_TMP_ROOT" \
        "$TERRAFORM_IMAGE" \
        -c 'apk add --no-cache bash curl git jq coreutils && touch /tmp/harness-ready && exec tail -f /dev/null' \
        2>"$startup_log"); then
        fail "could not start Terraform test container: $(<"$startup_log")"
    fi

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
    unset PROCESS_TERRAFORM_FMT PROCESS_TERRAFORM_VERSION
    unset PROCESS_TERRAFORM_INIT_UPGRADE
    unset PROCESS_TERRAFORM_ENV PROCESS_TERRAFORM_SECRET_ENV
    unset TF_CLI_CONFIG_FILE
    PROCESS_PATH_PREFIX=""
    PROCESS_TEST_CALL_LOG=""
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
    PROCESS_RESULT_DIR="$PROCESS_RUNNER_TEMP/result"
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


run_processing() {
    ensure_processing_container
    processing_shared_docker_env
    local -a upgrade_environment=()
    if [[ -v PROCESS_TERRAFORM_INIT_UPGRADE ]]; then
        upgrade_environment=(--env "PROCESS_TERRAFORM_INIT_UPGRADE=$PROCESS_TERRAFORM_INIT_UPGRADE")
    fi
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
        --env "PROCESS_TERRAFORM_FMT=${PROCESS_TERRAFORM_FMT-false}" \
        --env "PROCESS_TERRAFORM_ENV=${PROCESS_TERRAFORM_ENV-}" \
        --env "PROCESS_TERRAFORM_SECRET_ENV=${PROCESS_TERRAFORM_SECRET_ENV-}" \
        "${upgrade_environment[@]}" \
        --env "TF_CLI_CONFIG_FILE=${TF_CLI_CONFIG_FILE-}" \
        --env "PROCESS_PREPARATION_DEADLINE_EPOCH=${PROCESS_PREPARATION_DEADLINE_EPOCH-$(($(date +%s) + 1200))}" \
        --env "PROCESS_RESULT_DIR=${PROCESS_RESULT_DIR-$PROCESS_RUNNER_TEMP/result}" \
        --env "PROCESS_TEST_CALL_LOG=${PROCESS_TEST_CALL_LOG-}" \
        "$PROCESS_CONTAINER_ID" \
        /bin/bash "$PROCESS_SCRIPT" process
}


# close-stdout runs the script with its standard output closed, so writing a mask fails.
run_processing_mask() {
    ensure_processing_container
    local -a command=(/bin/bash "$PROCESS_SCRIPT" mask)
    [[ "${1-}" != close-stdout ]] || command=(/bin/bash -c 'exec "$@" >&-' bash "${command[@]}")
    docker exec \
        --user "$(id -u):$(id -g)" \
        --env "PROCESS_TERRAFORM_ENV=${PROCESS_TERRAFORM_ENV-}" \
        --env "PROCESS_TERRAFORM_SECRET_ENV=${PROCESS_TERRAFORM_SECRET_ENV-}" \
        "$PROCESS_CONTAINER_ID" \
        "${command[@]}"
}


assert_silent_success() {
    local description=$1 stdout_file=$2 stderr_file=$3
    shift 3
    if ! "$@" >"$stdout_file" 2>"$stderr_file"; then
        if [[ -f "$PROCESS_RESULT_DIR/result.json" ]]; then
            cat "$PROCESS_RESULT_DIR/result.json" >&2
            find "$PROCESS_RESULT_DIR/logs" -type f -name '*.log' -exec tail -n 12 {} \; >&2
        fi
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
    assert_command_failure run_processing "$PROCESS_TMP_ROOT" \
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


assert_discovery_failure() {
    assert_command_failure run_discovery "$DISCOVERY_TMP_ROOT" \
        "emitted JSON" "$1" "$2"
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


test_processing_init_upgrade_is_opt_in() {
    # Production break caught: plain init upgrades locked providers, an explicit upgrade
    # is ignored, or a locked-version conflict silently enables upgrade.
    local mode expected_version mirror newer_package
    for mode in unset false true conflict conflict-upgrade; do
        setup_processing_workspace
        configure_validation_provider_base
        mirror="$PROCESS_VALIDATION_FIXTURE_ROOT/provider-mirror/registry.terraform.io/yesdevnull/test"
        chmod u+w "$mirror"
        newer_package="$mirror/0.2.0/linux_amd64"
        mkdir -p "$newer_package"
        cp "$mirror/0.1.0/linux_amd64/terraform-provider-test_v0.1.0_x5" \
            "$newer_package/terraform-provider-test_v0.2.0_x5"
        chmod -R a-w "$mirror"
        expected_version=0.1.0
        case "$mode" in
            false) PROCESS_TERRAFORM_INIT_UPGRADE=false ;;
            true|conflict-upgrade)
                PROCESS_TERRAFORM_INIT_UPGRADE=true
                expected_version=0.2.0
                ;;
        esac
        if [[ "$mode" == conflict* ]]; then
            printf '%s\n' 'providers:' '  - name: test' '    version: "0.2.0"' \
                >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
            "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- .github/tf-version-bump/test.yml
            fixture_commit "$PROCESS_CONTROL_CHECKOUT" "Processing Test" \
                "processing-test@example.invalid" "test: request a newer locked provider"
        else
            sed -i.bak 's/version = "0.1.0"/version = ">= 0.1.0"/' "$PROCESS_TARGET_CHECKOUT/root/main.tf"
            rm "$PROCESS_TARGET_CHECKOUT/root/main.tf.bak"
            "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root/main.tf
            fixture_commit "$PROCESS_TARGET_CHECKOUT" "Processing Test" \
                "processing-test@example.invalid" "test: allow both provider releases"
        fi
        if [[ "$mode" == conflict ]]; then
            assert_processing_failure 'processing status error: terraform init failed for Terraform root root' \
                'locked provider conflict without upgrade'
            grep -F 'does not match configured version constraint' "$PROCESS_RESULT_DIR/logs/init-1.log" >/dev/null \
                || fail "plain init did not report the locked provider conflict"
            jq -e '.classification == "branch-init" and
                .failure.stage == "terraform init"' \
                "$PROCESS_RESULT_DIR/result.json" >/dev/null \
                || fail "init failure did not record the actual non-upgrade command"
            continue
        fi
        assert_silent_success "init upgrade $mode" "$PROCESS_TMP_ROOT/init.stdout" \
            "$PROCESS_TMP_ROOT/init.stderr" run_processing
        grep -F "version     = \"$expected_version\"" "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" >/dev/null \
            || fail "init upgrade $mode did not select provider $expected_version"

    done
}


test_processing_container_setup_captures_pull_progress() {
    cleanup_processing_container
    # Always pull the real pinned image so this also exercises setup output on a warm cache.
    assert_silent_success 'container setup with image pull' "$TEST_TMP_ROOT/setup.stdout" \
        "$TEST_TMP_ROOT/setup.stderr" ensure_processing_container always
    [[ "$(docker inspect --format='{{.State.Running}}' "$PROCESS_CONTAINER_ID")" == true ]] \
        || fail 'processing container did not start'
    grep -F "${TERRAFORM_IMAGE#*@}" "$TEST_TMP_ROOT/container-start.log" >/dev/null \
        || fail 'image pull diagnostics were not captured'
    cleanup_processing_container
    if (ensure_processing_container invalid-policy) >"$TEST_TMP_ROOT/setup.stdout" 2>"$TEST_TMP_ROOT/setup.stderr"; then
        fail 'invalid Docker pull policy was accepted'
    fi
    [[ ! -s "$TEST_TMP_ROOT/setup.stdout" ]] || fail 'failed setup emitted stdout'
    grep -F 'could not start Terraform test container:' "$TEST_TMP_ROOT/setup.stderr" >/dev/null \
        || fail 'container startup failure was not reported'
    grep -F 'invalid-policy' "$TEST_TMP_ROOT/setup.stderr" >/dev/null \
        || fail 'container startup failure lost Docker diagnostics'
}

test_processing_combines_update_init_format_validate() {
    setup_processing_workspace
    PROCESS_TERRAFORM_FMT=true
    mkdir -p "$PROCESS_TARGET_CHECKOUT/root/nested" "$PROCESS_TARGET_CHECKOUT/second"
    printf '%s\n' 'locals { value={ a="b" } }' >"$PROCESS_TARGET_CHECKOUT/root/nested/child.tf"
    printf '%s\n' 'terraform { required_version = ">= 1.0" }' >"$PROCESS_TARGET_CHECKOUT/second/main.tf"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root/nested/child.tf second/main.tf
    fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: add multiple roots and formatting input'
    PROCESS_TERRAFORM_ROOTS=$'root\nsecond'
    local base_oid
    base_oid=$(processing_base_oid)
    assert_silent_success 'combined processing' "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
    jq -e --arg base "$base_oid" '.schema_version == 3 and .classification == "success" and
        .base_oid == $base and .roots == ["root", "second"] and (.patch_sha256 | length == 64)' \
        "$PROCESS_RESULT_DIR/result.json" >/dev/null || fail 'missing combined result contract'
    [[ "$(processing_base_oid)" == "$base_oid" ]] || fail 'processing created a commit'
    [[ -f "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'missing final patch'
    [[ "$(find "$PROCESS_RESULT_DIR" -maxdepth 1 -name '*.patch' | wc -l | tr -d ' ')" == 1 ]] || fail 'multiple patches emitted'
    "$TEST_GIT" clone --quiet "$PROCESS_TARGET_CHECKOUT" "$PROCESS_TMP_ROOT/applied"
    "$TEST_GIT" -C "$PROCESS_TMP_ROOT/applied" apply --index "$PROCESS_RESULT_DIR/candidate.patch"
    local file
    for file in root/main.tf second/main.tf root/nested/child.tf; do
        cmp "$PROCESS_TMP_ROOT/applied/$file" "$PROCESS_TARGET_CHECKOUT/$file" || fail 'patch differs from validated candidate'
    done
    grep -F '>= 1.15.0' "$PROCESS_TMP_ROOT/applied/second/main.tf" >/dev/null || fail 'second root was not updated'
    grep -F 'value = { a = "b" }' "$PROCESS_TMP_ROOT/applied/root/nested/child.tf" >/dev/null || fail 'recursive formatting missing from patch'
    # The report step summarises the result directory processing really wrote.
    assert_silent_success 'reporting the combined result' "$PROCESS_TMP_ROOT/report.stdout" \
        "$PROCESS_TMP_ROOT/report.stderr" \
        run_report_step "$PROCESS_RESULT_DIR/result.json" success "$PROCESS_TMP_ROOT/summary.md"
    local root
    for root in root second; do
        grep -qxF "#### Updates in \`$root\`" "$PROCESS_TMP_ROOT/summary.md" \
            || fail "the summary of a real result omits root $root: $(<"$PROCESS_TMP_ROOT/summary.md")"
    done
    grep -qF "Updated Terraform required_version to '>= 1.15.0' in main.tf" "$PROCESS_TMP_ROOT/summary.md" \
        || fail "the summary of a real result omits the updater's output: $(<"$PROCESS_TMP_ROOT/summary.md")"
}

test_processing_updates_dependent_roots_before_init() {
    # Regression: initialising a parent before updating its local module caches old registry versions.
    setup_processing_workspace
    mkdir "$PROCESS_TARGET_CHECKOUT/shared"
    printf '%s\n' 'module "shared" { source = "../shared" }' >>"$PROCESS_TARGET_CHECKOUT/root/main.tf"
    cat >"$PROCESS_TARGET_CHECKOUT/shared/main.tf" <<'EOF'
module "templates" {
  source   = "hashicorp/dir/template"
  version  = "1.0.1"
  base_dir = path.module
}
EOF
    printf '%s\n' 'modules:' '  - source: hashicorp/dir/template' '    version: "1.0.2"' \
        >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
    "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- .github/tf-version-bump/test.yml
    fixture_commit "$PROCESS_CONTROL_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: bump nested registry module'
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root/main.tf shared/main.tf
    fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: reference a later configured root'
    PROCESS_TERRAFORM_ROOTS=$'root\nshared'
    assert_silent_success 'dependent roots' "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
    jq -e '.classification == "success"' "$PROCESS_RESULT_DIR/result.json" >/dev/null \
        || fail 'dependent roots did not produce a validated candidate'
    "$TEST_GIT" clone --quiet "$PROCESS_TARGET_CHECKOUT" "$PROCESS_TMP_ROOT/applied"
    "$TEST_GIT" -C "$PROCESS_TMP_ROOT/applied" apply --index "$PROCESS_RESULT_DIR/candidate.patch"
    grep -F '"1.0.2"' "$PROCESS_TMP_ROOT/applied/shared/main.tf" >/dev/null \
        || fail 'candidate omitted nested module update'
    cmp "$PROCESS_TARGET_CHECKOUT/shared/main.tf" "$PROCESS_TMP_ROOT/applied/shared/main.tf" \
        || fail 'candidate differs from validated module'
}

test_processing_validates_unchanged_candidates() {
    local mode
    for mode in valid invalid; do
        setup_processing_workspace
        printf '%s\n' 'terraform_version: ">= 1.0"' >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
        "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- .github/tf-version-bump/test.yml
        fixture_commit "$PROCESS_CONTROL_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: request unchanged version'
        if [[ "$mode" == invalid ]]; then
            printf '%s\n' 'resource "terraform_data" "bad" { nonexistent = true }' >>"$PROCESS_TARGET_CHECKOUT/root/main.tf"
            "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root/main.tf
            fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: add invalid unchanged resource'
            assert_processing_failure 'processing status error: terraform validate failed for Terraform root root' 'unchanged invalid candidate'
            jq -e '.classification == "branch-validation" and .failure.stage == "terraform validate" and .failure.root == "root" and .failure.status > 0' "$PROCESS_RESULT_DIR/result.json" >/dev/null
            grep -F 'Unsupported argument' "$PROCESS_RESULT_DIR/logs/validate-1.log" >/dev/null
        else
            assert_silent_success 'unchanged valid candidate' "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
            jq -e '.classification == "no-change"' "$PROCESS_RESULT_DIR/result.json" >/dev/null
            [[ -s "$PROCESS_RESULT_DIR/logs/validate-1.log" ]] || fail 'no-change skipped validation'
        fi
        [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'unchanged or failed candidate emitted patch'
    done
}

test_workflow_runs_three_jobs_with_current_attempt_results() {
    yq -o=json '.jobs' "$REUSABLE_WORKFLOW" | jq -e '
        keys == ["discover", "process", "publish"] and
        .process.needs == "discover" and .publish.needs == ["discover", "process"] and
        .process.permissions == {contents:"read"} and .publish.permissions.contents == "write" and
        ([.process.steps[] | select(.env.PROCESS_RESULT_DIR)] | length == 1) and
        ([.process.steps[] | select(.with.name != null) | .with.name] ==
         [.publish.steps[] | select(.with.name != null) | .with.name]) and
        ([.publish.steps[] | select(.env.TF_TOKEN_app_terraform_io != null)] | length == 0) and
        ([.publish.steps[] | select(.env.RECONCILE_RUN_URL != null)] | length == 1)
    ' >/dev/null || fail 'workflow does not wire the three-job current-attempt result contract'
}

test_processing_records_real_update_and_format_failures() {
    local mode stage classification log
    for mode in update format; do
        setup_processing_workspace
        if [[ "$mode" == update ]]; then
            printf 'terraform { invalid\n' >"$PROCESS_TARGET_CHECKOUT/root/main.tf"
            stage=tf-version-bump
            classification='branch-update'
            log=update-1.log
        else
            mkdir "$PROCESS_TARGET_CHECKOUT/root/nested"
            printf 'locals { invalid\n' >"$PROCESS_TARGET_CHECKOUT/root/nested/child.tf"
            PROCESS_TERRAFORM_FMT=true
            stage='terraform fmt'
            classification='branch-format'
            log=fmt-1.log
        fi
        "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add --all -- .
        fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' "test: invalid $mode input"
        assert_processing_failure "processing status error: $stage failed for Terraform root root" "$mode failure"
        jq -e --arg c "$classification" '.classification == $c and .failure.status > 0' "$PROCESS_RESULT_DIR/result.json" >/dev/null
        [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'failed processing emitted patch'
        grep -E 'Error|error' "$PROCESS_RESULT_DIR/logs/$log" >/dev/null || fail 'missing command diagnostics'
    done
}

test_processing_rejects_invalid_inputs_before_updates() {
    local mode expected
    for mode in boolean-upgrade boolean-format root-absolute root-traversal root-missing duplicate config-absolute symlink control-oid base-oid hash deadline; do
        setup_processing_workspace
        case "$mode" in
            boolean-upgrade) PROCESS_TERRAFORM_INIT_UPGRADE=TRUE; expected='Terraform init upgrade must be true or false' ;;
            boolean-format) PROCESS_TERRAFORM_FMT=1; expected='Terraform formatting must be true or false' ;;
            root-absolute) PROCESS_TERRAFORM_ROOTS=/tmp; expected='Terraform root must be repository-relative' ;;
            root-traversal) PROCESS_TERRAFORM_ROOTS=../control; expected="Terraform root must not contain '..'" ;;
            root-missing) PROCESS_TERRAFORM_ROOTS=missing; expected='Terraform root does not exist' ;;
            duplicate) PROCESS_TERRAFORM_ROOTS=$'root\n./root'; expected='duplicate canonical Terraform root' ;;
            config-absolute) PROCESS_CONFIG_PATH=/tmp/config; expected='config path must be repository-relative' ;;
            symlink)
                ln -s "$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml" "$PROCESS_TARGET_CHECKOUT/root/escape.tf"
                "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root/escape.tf
                fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: escaping Terraform file'
                expected='Terraform file must be a regular non-symlink'
                ;;
            control-oid) PROCESS_CONTROL_OID=0000000000000000000000000000000000000000; expected='control checkout HEAD does not match control OID' ;;
            base-oid) PROCESS_BASE_OID=0000000000000000000000000000000000000000; expected='target checkout HEAD does not match base OID' ;;
            hash) PROCESS_REF_HASH=bad; expected='state ref hash does not match ref hash' ;;
            deadline) PROCESS_PREPARATION_DEADLINE_EPOCH=1; expected='processing deadline expired before workspace setup' ;;
        esac
        assert_processing_failure "$expected" "$mode input"
        [[ -z "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" status --porcelain)" ]] || fail 'invalid input modified target'
        [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'invalid input produced patch'
    done
}

test_processing_invalid_config_reconciles_branch_failure() {
    local mode diagnostic
    for mode in malformed unknown-field; do
        setup_processing_workspace
        if [[ "$mode" == malformed ]]; then
            printf 'modules: [\n' >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
            diagnostic='did not find expected node content'
        else
            printf 'unknown: true\n' >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
            diagnostic='field unknown not found'
        fi
        "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- .github/tf-version-bump/test.yml
        fixture_commit "$PROCESS_CONTROL_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' "test: $mode bump configuration"
        assert_processing_failure 'tf-version-bump failed for Terraform root root' "$mode configuration"
        jq -e '.classification == "branch-update" and .failure.stage == "tf-version-bump" and
            .failure.root == "root" and .failure.status > 0' "$PROCESS_RESULT_DIR/result.json" >/dev/null
        grep -F "$diagnostic" "$PROCESS_RESULT_DIR/logs/update-1.log" >/dev/null || fail 'configuration diagnostic missing'
        [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'invalid configuration emitted a patch'
        [[ -z "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" status --porcelain)" ]] || fail 'invalid configuration modified target'
        if ! PROCESSING_RESULT_FIXTURE="$PROCESS_RESULT_DIR" PROCESSING_TARGET_FIXTURE="$PROCESS_TARGET_CHECKOUT" \
            "$RECONCILE_TEST" test_reconciles_supplied_processing_failure \
            >"$PROCESS_TMP_ROOT/lifecycle.stdout" 2>"$PROCESS_TMP_ROOT/lifecycle.stderr"; then
            fail "invalid configuration lifecycle failed: $(<"$PROCESS_TMP_ROOT/lifecycle.stderr")"
        fi
        [[ ! -s "$PROCESS_TMP_ROOT/lifecycle.stderr" ]] || fail 'failure lifecycle emitted unexpected diagnostics'
    done
}

test_processing_rejects_ignored_generated_lock() {
    setup_processing_workspace
    configure_validation_provider_base_without_lock
    printf '.terraform.lock.hcl\n' >"$PROCESS_TARGET_CHECKOUT/.gitignore"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- .gitignore
    fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: ignore provider lock'
    assert_processing_failure 'required provider lock file is ignored' 'ignored lock'
    jq -e '.classification == "automation"' "$PROCESS_RESULT_DIR/result.json" >/dev/null
    [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'ignored lock became publishable'
}

test_processing_formats_only_after_dependency_or_lock_changes() {
    local mode mirror newer_package original_lock
    for mode in unchanged new-lock upgraded-lock; do
        setup_processing_workspace
        PROCESS_TERRAFORM_FMT=true
        printf '%s\n' 'terraform_version: ">= 1.0"' >"$PROCESS_CONTROL_CHECKOUT/.github/tf-version-bump/test.yml"
        "$TEST_GIT" -C "$PROCESS_CONTROL_CHECKOUT" add -- .github/tf-version-bump/test.yml
        fixture_commit "$PROCESS_CONTROL_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: keep requested Terraform version unchanged'
        if [[ "$mode" == new-lock ]]; then
            configure_validation_provider_base_without_lock
        elif [[ "$mode" == upgraded-lock ]]; then
            configure_validation_provider_base
            mirror="$PROCESS_VALIDATION_FIXTURE_ROOT/provider-mirror/registry.terraform.io/yesdevnull/test"
            chmod u+w "$mirror"
            newer_package="$mirror/0.2.0/linux_amd64"
            mkdir -p "$newer_package"
            cp "$mirror/0.1.0/linux_amd64/terraform-provider-test_v0.1.0_x5" \
                "$newer_package/terraform-provider-test_v0.2.0_x5"
            chmod -R a-w "$mirror"
            sed -i.bak 's/version = "0.1.0"/version = ">= 0.1.0"/' "$PROCESS_TARGET_CHECKOUT/root/main.tf"
            rm "$PROCESS_TARGET_CHECKOUT/root/main.tf.bak"
            PROCESS_TERRAFORM_INIT_UPGRADE=true
        fi
        mkdir "$PROCESS_TARGET_CHECKOUT/root/nested"
        printf '%s\n' 'locals { value={ a="b" } }' >"$PROCESS_TARGET_CHECKOUT/root/nested/child.tf"
        "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root
        fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' "test: $mode formatting candidate"
        cp "$PROCESS_TARGET_CHECKOUT/root/main.tf" "$PROCESS_TMP_ROOT/original-main.tf"
        cp "$PROCESS_TARGET_CHECKOUT/root/nested/child.tf" "$PROCESS_TMP_ROOT/original-child.tf"
        original_lock=''
        if [[ -f "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" ]]; then
            original_lock=$(sha256_file "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl")
        fi
        assert_silent_success "$mode formatting candidate" "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
        cmp "$PROCESS_TMP_ROOT/original-main.tf" "$PROCESS_TARGET_CHECKOUT/root/main.tf" || fail 'lock-only case changed Terraform constraints'
        if [[ "$mode" == unchanged ]]; then
            jq -e '.classification == "no-change"' "$PROCESS_RESULT_DIR/result.json" >/dev/null || fail 'unchanged dependencies produced a formatting-only candidate'
            cmp "$PROCESS_TMP_ROOT/original-child.tf" "$PROCESS_TARGET_CHECKOUT/root/nested/child.tf" || fail 'unchanged dependencies triggered formatting'
            [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'unchanged dependencies emitted a patch'
        else
            [[ "$(sha256_file "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl")" != "$original_lock" ]] || fail 'lock fixture did not change'
            grep -F 'value = { a = "b" }' "$PROCESS_TARGET_CHECKOUT/root/nested/child.tf" >/dev/null || fail 'lock-only change did not enable formatting'
            jq -e '.classification == "success"' "$PROCESS_RESULT_DIR/result.json" >/dev/null
            "$TEST_GIT" clone --quiet "$PROCESS_TARGET_CHECKOUT" "$PROCESS_TMP_ROOT/applied"
            "$TEST_GIT" -C "$PROCESS_TMP_ROOT/applied" apply --index "$PROCESS_RESULT_DIR/candidate.patch"
            cmp "$PROCESS_TARGET_CHECKOUT/root/.terraform.lock.hcl" "$PROCESS_TMP_ROOT/applied/root/.terraform.lock.hcl" || fail 'patch omitted lock change'
            cmp "$PROCESS_TARGET_CHECKOUT/root/nested/child.tf" "$PROCESS_TMP_ROOT/applied/root/nested/child.tf" || fail 'patch omitted formatting'
        fi
        [[ -s "$PROCESS_RESULT_DIR/logs/validate-1.log" ]] || fail 'formatting gate skipped validation'
    done
}

test_processing_rejects_formatter_changes_outside_patch_policy() {
    setup_processing_workspace
    PROCESS_TERRAFORM_FMT=true
    printf 'value={a="b"}\n' >"$PROCESS_TARGET_CHECKOUT/root/input.tfvars"
    "$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" add -- root/input.tfvars
    fixture_commit "$PROCESS_TARGET_CHECKOUT" 'Processing Test' 'processing-test@example.invalid' 'test: formatter changes non-Terraform path'
    assert_processing_failure 'formatting changed a non-Terraform path' 'unexpected candidate path'
    jq -e '.classification == "automation"' "$PROCESS_RESULT_DIR/result.json" >/dev/null
    [[ -s "$PROCESS_RESULT_DIR/logs/fmt-1.log" ]] || fail 'automation failure lost diagnostics'
    [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'unexpected path became publishable'
}

test_processing_rejects_files_created_during_the_run() {
    setup_processing_workspace
    configure_validation_provider_base
    # A supplied variable can make a Terraform command create a file inside the
    # checkout; only terraform init's lock file may appear during a run.
    PROCESS_TERRAFORM_ENV="TEST_OBSERVATION_PATH=$PROCESS_TARGET_CHECKOUT/root/observed.tf"
    assert_processing_failure 'candidate created a path that is not a provider lock file' \
        'file created during the run'
    jq -e '.classification == "automation"' "$PROCESS_RESULT_DIR/result.json" >/dev/null
    [[ ! -e "$PROCESS_RESULT_DIR/candidate.patch" ]] || fail 'a file created during the run became publishable'
}

test_processing_supplies_terraform_environment_to_commands() {
    setup_processing_workspace
    configure_validation_provider_base
    local observation="$PROCESS_RUNNER_TEMP/observed-environment"
    # terraform validate launches the fixture provider, which records the
    # environment it was handed; the path and one channel arrive as inputs and
    # the other channel as a secret, so a missing channel records as empty.
    PROCESS_TERRAFORM_ENV="TEST_OBSERVATION_PATH=$observation"$'\nTEST_INPUT_CHANNEL=input-delivered'
    PROCESS_TERRAFORM_SECRET_ENV='TEST_SECRET_CHANNEL=secret-delivered'
    assert_silent_success 'supplied environment' "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
    diff - "$observation" >/dev/null <<'EOF' || fail 'supplied environment did not reach Terraform'
input=input-delivered secret=secret-delivered
EOF
}

# The GitHub provider reads its App credentials from GITHUB_ names, and its PEM is
# multi-line, so a supplied credential exercises the prefix and the escapes together.
# GITHUB_APP_ID rides along unobserved: the run succeeding proves it is accepted.
test_processing_delivers_escaped_github_app_credentials() {
    setup_processing_workspace
    configure_validation_provider_base
    local observation="$PROCESS_RUNNER_TEMP/observed-environment"
    PROCESS_TERRAFORM_ENV="TEST_OBSERVATION_PATH=$observation"$'\nTEST_EXACT_NAME=GITHUB_APP_PEM_FILE\nGITHUB_APP_ID=123456'
    PROCESS_TERRAFORM_SECRET_ENV='GITHUB_APP_PEM_FILE=-----BEGIN TEST KEY-----\nkey-material\\nnot-a-newline\tnot-a-tab\n-----END TEST KEY-----\n'
    assert_silent_success 'GitHub App credentials' "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
    # The quoted heredoc keeps its backslashes literal and diff reports a missing final
    # newline, so this compares the exact bytes, including the trailing newline.
    diff - "$observation.exact" >/dev/null <<'EOF' || fail "the GitHub App credential did not reach Terraform intact: $(od -c "$observation.exact" 2>&1)"
-----BEGIN TEST KEY-----
key-material\nnot-a-newline\tnot-a-tab
-----END TEST KEY-----
EOF
}

# The updater runs from an absolute path, so no PATH entry can intercept it; the
# timeout that run_bounded wraps every command with can be. timeout execs its command
# without altering the environment, and an env wrapper's assignments are visible in its
# arguments, so the recorder sees exactly what each command is handed.
configure_command_environment_recorder() {
    local recorder_bin="$PROCESS_TMP_ROOT/recorder-bin"
    mkdir "$recorder_bin"
    cat >"$recorder_bin/timeout" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
# run_bounded calls timeout with options, a duration, then the command and its arguments.
index=1
while [[ "${!index}" == -* ]]; do index=$((index + 1)); done
index=$((index + 1))
input=${TEST_INPUT_CHANNEL+delivered}
secret=${TEST_SECRET_CHANNEL+delivered}
# terraform_command runs `env -- NAME=VALUE ... terraform`, so read past the wrapper to
# the command it launches and count its assignments as part of that command's environment.
if [[ "${!index##*/}" == env ]]; then
    index=$((index + 1))
    [[ "${!index}" != -- ]] || index=$((index + 1))
    while [[ "${!index}" == *=* ]]; do
        case "${!index}" in
            TEST_INPUT_CHANNEL=*) input=delivered ;;
            TEST_SECRET_CHANNEL=*) secret=delivered ;;
        esac
        index=$((index + 1))
    done
fi
command=${!index##*/}
# A Terraform command is named by its first argument that is not an option such as -chdir.
if [[ "$command" == terraform ]]; then
    index=$((index + 1))
    while [[ "${!index}" == -* ]]; do index=$((index + 1)); done
    command+=" ${!index}"
fi
printf '%s input=%s secret=%s\n' "$command" "$input" "$secret" >>"${PROCESS_TEST_CALL_LOG:?}"
PATH=${PATH#*:}
exec timeout "$@"
EOF
    chmod 755 "$recorder_bin/timeout"
    PROCESS_PATH_PREFIX=$recorder_bin
    PROCESS_TEST_CALL_LOG="$PROCESS_TMP_ROOT/command-environments.log"
    : >"$PROCESS_TEST_CALL_LOG"
}


test_processing_scopes_supplied_environment_to_init_fmt_and_validate() {
    setup_processing_workspace
    configure_command_environment_recorder
    # The updater changes the fixture's required_version, so formatting runs as well.
    PROCESS_TERRAFORM_FMT=true
    PROCESS_TERRAFORM_ENV='TEST_INPUT_CHANNEL=input-delivered'
    PROCESS_TERRAFORM_SECRET_ENV='TEST_SECRET_CHANNEL=secret-delivered'
    assert_silent_success 'scoped environment' "$PROCESS_TMP_ROOT/stdout" "$PROCESS_TMP_ROOT/stderr" run_processing
    # Every bounded command is listed, so a missing line is a command that did not run,
    # and the delivered lines prove the recorder observes delivery through each channel.
    LC_ALL=C sort -u "$PROCESS_TEST_CALL_LOG" >"$PROCESS_TMP_ROOT/command-environments.sorted"
    diff - "$PROCESS_TMP_ROOT/command-environments.sorted" >/dev/null <<'EOF' \
        || fail "the supplied variables did not reach exactly terraform init, fmt and validate: $(<"$PROCESS_TMP_ROOT/command-environments.sorted")"
curl input= secret=
terraform fmt input=delivered secret=delivered
terraform init input=delivered secret=delivered
terraform validate input=delivered secret=delivered
terraform version input= secret=
tf-version-bump input= secret=
EOF
}

test_processing_masks_only_secret_environment_values() {
    setup_processing_workspace
    # Exact reserved names such as ENV and GITHUB_ENV must not block longer names.
    PROCESS_TERRAFORM_ENV=$'TF_VAR_region=ap-southeast-2\nENVIRONMENT=prod\nGITHUB_ENVIRONMENT=x'
    # Another registry's token stays a legitimate secret, and a per cent sign must
    # be encoded, because the runner unescapes workflow command data. A multi-line
    # value registers one encoded mask, never one per line: a short line would
    # redact every occurrence of itself throughout the log. A value keeps every '='
    # after the one that ends its name, as base64 padding needs.
    PROCESS_TERRAFORM_SECRET_ENV=$'AWS_ACCESS_KEY_ID=AKIAEXAMPLE\nAWS_SECRET_ACCESS_KEY=example-secret\nTF_TOKEN_other_example_com=other-registry-token\nTF_VAR_discount=100%off\nGITHUB_APP_PEM_FILE=first-line\\nsecond-line\\n\nTF_VAR_padded=YWJjZA=='
    run_processing_mask >"$PROCESS_TMP_ROOT/mask.stdout" 2>"$PROCESS_TMP_ROOT/mask.stderr"
    [[ ! -s "$PROCESS_TMP_ROOT/mask.stderr" ]] \
        || fail "masking emitted diagnostics: $(<"$PROCESS_TMP_ROOT/mask.stderr")"
    diff - "$PROCESS_TMP_ROOT/mask.stdout" >/dev/null <<'EOF' || fail 'masking did not register exactly the secret values'
::add-mask::AKIAEXAMPLE
::add-mask::example-secret
::add-mask::other-registry-token
::add-mask::100%25off
::add-mask::first-line%0Asecond-line%0A
::add-mask::YWJjZA==
EOF
    # A mask that cannot be written must fail the step: processing would otherwise
    # succeed while the value it guards reached the console unredacted.
    ! run_processing_mask close-stdout >"$PROCESS_TMP_ROOT/closed-mask.stdout" \
        2>"$PROCESS_TMP_ROOT/closed-mask.stderr" \
        || fail 'masking succeeded without writing its masks'
    grep -qF 'write error' "$PROCESS_TMP_ROOT/closed-mask.stderr" \
        || fail "masking did not report its failed write: $(<"$PROCESS_TMP_ROOT/closed-mask.stderr")"
}

test_processing_rejects_invalid_terraform_environment() {
    local mode expected
    for mode in syntax name reserved-prefix reserved-name reserved-log-path reserved-plugin-cache \
        reserved-registry-token reserved-registry-token-upper-case reserved-registry-token-mixed-case \
        reserved-runner-env reserved-runner-path reserved-runner-output reserved-runner-summary \
        reserved-runner-state carriage-return duplicate cross-source; do
        setup_processing_workspace
        # A valid secret precedes every rejected entry, so masking one would show below,
        # and every rejected entry carries the same value, so the leak checks below
        # would observe a diagnostic that printed the entry it rejects.
        PROCESS_TERRAFORM_SECRET_ENV='TF_VAR_secret=undisclosed-value'
        case "$mode" in
            # A PEM pasted without escapes leaves a continuation line with no '='.
            syntax)
                PROCESS_TERRAFORM_SECRET_ENV+=$'\nundisclosed-value'
                expected='Terraform environment entries must be one NAME=VALUE per line; write a newline inside a value as \n'
                ;;
            name) PROCESS_TERRAFORM_ENV='2BAD=undisclosed-value'; expected='Terraform environment entries must be one NAME=VALUE per line' ;;
            reserved-prefix) PROCESS_TERRAFORM_ENV='PROCESS_RESULT_DIR=undisclosed-value'; expected='Terraform environment name PROCESS_RESULT_DIR is reserved' ;;
            reserved-name) PROCESS_TERRAFORM_ENV='TF_DATA_DIR=undisclosed-value'; expected='Terraform environment name TF_DATA_DIR is reserved' ;;
            reserved-log-path) PROCESS_TERRAFORM_ENV='TF_LOG_PATH=undisclosed-value'; expected='Terraform environment name TF_LOG_PATH is reserved' ;;
            reserved-plugin-cache) PROCESS_TERRAFORM_ENV='TF_PLUGIN_CACHE_DIR=undisclosed-value'; expected='Terraform environment name TF_PLUGIN_CACHE_DIR is reserved' ;;
            reserved-registry-token) PROCESS_TERRAFORM_ENV='TF_TOKEN_app_terraform_io=undisclosed-value'; expected='Terraform environment name TF_TOKEN_app_terraform_io is reserved' ;;
            # Terraform lowercases the host a TF_TOKEN_ name encodes, so every letter case
            # of the reserved name would shadow the injected registry token as well.
            reserved-registry-token-upper-case) PROCESS_TERRAFORM_ENV='TF_TOKEN_APP_TERRAFORM_IO=undisclosed-value'; expected='Terraform environment name TF_TOKEN_APP_TERRAFORM_IO is reserved' ;;
            reserved-registry-token-mixed-case) PROCESS_TERRAFORM_ENV='TF_TOKEN_App_Terraform_Io=undisclosed-value'; expected='Terraform environment name TF_TOKEN_App_Terraform_Io is reserved' ;;
            reserved-runner-env) PROCESS_TERRAFORM_ENV='GITHUB_ENV=undisclosed-value'; expected='Terraform environment name GITHUB_ENV is reserved' ;;
            reserved-runner-path) PROCESS_TERRAFORM_ENV='GITHUB_PATH=undisclosed-value'; expected='Terraform environment name GITHUB_PATH is reserved' ;;
            reserved-runner-output) PROCESS_TERRAFORM_ENV='GITHUB_OUTPUT=undisclosed-value'; expected='Terraform environment name GITHUB_OUTPUT is reserved' ;;
            reserved-runner-summary) PROCESS_TERRAFORM_ENV='GITHUB_STEP_SUMMARY=undisclosed-value'; expected='Terraform environment name GITHUB_STEP_SUMMARY is reserved' ;;
            reserved-runner-state) PROCESS_TERRAFORM_ENV='GITHUB_STATE=undisclosed-value'; expected='Terraform environment name GITHUB_STATE is reserved' ;;
            carriage-return)
                PROCESS_TERRAFORM_SECRET_ENV=$'TF_VAR_secret=undisclosed-value\r'
                expected='Terraform environment value for TF_VAR_secret must not contain a carriage return'
                ;;
            duplicate) PROCESS_TERRAFORM_ENV=$'TF_VAR_a=1\nTF_VAR_a=undisclosed-value'; expected='Terraform environment name TF_VAR_a is set twice' ;;
            # The secret source is parsed first, so the input entry is the one rejected.
            cross-source)
                PROCESS_TERRAFORM_SECRET_ENV+=$'\nTF_VAR_a=1'
                PROCESS_TERRAFORM_ENV='TF_VAR_a=undisclosed-value'
                expected='Terraform environment name TF_VAR_a is set twice'
                ;;
        esac
        assert_processing_failure "$expected" "$mode environment entry"
        ! grep -qF 'undisclosed-value' "$PROCESS_TMP_ROOT/failure.stderr" \
            || fail 'processing diagnostics leaked a secret value'
        [[ -z "$("$TEST_GIT" -C "$PROCESS_TARGET_CHECKOUT" status --porcelain)" ]] \
            || fail "$mode environment entry was rejected only after the checkout changed"
        [[ ! -e "$PROCESS_RESULT_DIR/logs/download.log" ]] \
            || fail "$mode environment entry was rejected only after the release download"
        # The result file is what the artefact upload and publish job depend on.
        jq -e '.classification == "automation"' "$PROCESS_RESULT_DIR/result.json" >/dev/null \
            || fail "$mode environment entry left no automation result"
        # Masking runs before processing in one workflow run block, so a parse failure
        # there must not stop processing from reporting the diagnostic; it reports its
        # own name-only diagnostic to the step log rather than failing silently.
        run_processing_mask >"$PROCESS_TMP_ROOT/mask.stdout" 2>"$PROCESS_TMP_ROOT/mask.stderr" \
            || fail "$mode environment entry masking did not defer to processing"
        [[ ! -s "$PROCESS_TMP_ROOT/mask.stdout" ]] \
            || fail "$mode environment entry masking registered a mask: $(<"$PROCESS_TMP_ROOT/mask.stdout")"
        grep -qF "$expected" "$PROCESS_TMP_ROOT/mask.stderr" \
            || fail "$mode environment entry masking did not report '$expected': $(<"$PROCESS_TMP_ROOT/mask.stderr")"
        ! grep -qF 'undisclosed-value' \
            "$PROCESS_TMP_ROOT/mask.stdout" "$PROCESS_TMP_ROOT/mask.stderr" \
            || fail 'masking diagnostics leaked a secret value'
    done
}

# The README lists the reserved prefixes and names in prose, one term per pair of
# backticks, between the sentences these markers name.
readme_reserved_terms() {
    local start=$1 end=$2
    tr '\n' ' ' <"$SCRIPT_DIR/README.md" \
        | sed -n "s/.*$start\(.*\)$end.*/\1/p" \
        | grep -o "\`[^\`]*\`" | tr -d '`' | sort
}


script_reserved_terms() {
    local name=$1 assignment
    assignment=$(sed -n "/^$name=(/,/)/p" "$PROCESS_SCRIPT")
    # shellcheck disable=SC2030,SC2031 # The subshell keeps the parsed array out of the harness.
    ( eval "$assignment"; declare -n values="$name"; printf '%s\n' "${values[@]}" ) | sort
}


assert_documented_reserved_terms() {
    local name=$1 start=$2 end=$3 documented defined difference
    documented=$(readme_reserved_terms "$start" "$end")
    defined=$(script_reserved_terms "$name")
    [[ -n "$documented" && -n "$defined" ]] \
        || fail "no $name entries were parsed from the README or the script"
    difference=$(diff <(printf '%s\n' "$documented") <(printf '%s\n' "$defined")) \
        || fail "the README and the script disagree about $name:"$'\n'"$difference"
}


test_readme_documents_the_reserved_environment_names() {
    assert_documented_reserved_terms RESERVED_ENVIRONMENT_PREFIXES \
        'The reserved prefixes are' 'The reserved exact names are'
    assert_documented_reserved_terms RESERVED_ENVIRONMENT_NAMES \
        'The reserved exact names are' 'The last of those is reserved'
}


write_report_manifest() {
    local path=$1 classification=$2 failure=${3-null} roots=${4-[]}
    jq -n --arg branch 'state/nonproduction/example-thing' \
        --arg classification "$classification" --argjson failure "$failure" \
        --argjson roots "$roots" \
        '{schema_version: 3, state_branch: $branch, classification: $classification, roots: $roots}
         + (if $failure == null then {} else {failure: $failure} end)' >"$path"
}


# Runs one reusable-workflow step's run: body with the given NAME=VALUE environment.
# A step that cannot be found fails loudly instead of running an empty script.
run_workflow_step() {
    local job=$1 step=$2 summary=$3
    shift 3
    local body="$TEST_TMP_ROOT/workflow-step.sh"
    STEP_JOB="$job" STEP_NAME="$step" \
        yq -r '.jobs[strenv(STEP_JOB)].steps[] | select(.name == strenv(STEP_NAME)) | .run' \
        "$REUSABLE_WORKFLOW" >"$body"
    [[ -s "$body" ]] || fail "the $job job has no step named '$step' with a run body"
    : >"$summary"
    # GitHub Actions runs a run: body without a shell key under its documented default,
    # `bash -e {0}`; the runner's own semantics cannot be reproduced here beyond that.
    env "$@" GITHUB_STEP_SUMMARY="$summary" bash -e "$body"
}


run_report_step() {
    run_workflow_step process 'Report processing result' "$3" PROCESS_OUTCOME="$2" RESULT_MANIFEST="$1"
}


test_workflow_reports_the_processing_result() {
    # The report runs whatever processing did, judges it by the processing step's own
    # outcome, and reads the manifest from the directory processing writes.
    yq -o=json '.jobs.process.steps' "$REUSABLE_WORKFLOW" | jq -e '
        ([.[] | select(.id == "process")] | length == 1) as $single
        | (.[] | select(.id == "process") | .env.PROCESS_RESULT_DIR) as $result
        | [.[] | select(.name == "Report processing result")]
        | $single and length == 1 and .[0].if == "${{ always() }}"
          and .[0].env.PROCESS_OUTCOME == "${{ steps.process.outcome }}"
          and .[0].env.RESULT_MANIFEST == $result + "/result.json"
    ' >/dev/null || fail 'the report step is not wired to the processing step and its result'
    local work="$TEST_TMP_ROOT/report-step"
    rm -rf -- "$work"
    mkdir "$work"
    local manifest="$work/result.json" summary="$work/summary.md" diagnostics="$work/stderr"
    local report

    write_report_manifest "$manifest" branch-init \
        '{"stage": "terraform init", "root": "environments/production", "status": 1}'
    assert_silent_success 'reporting a branch failure' "$work/stdout" "$diagnostics" \
        run_report_step "$manifest" failure "$summary"
    report=$(<"$summary")
    [[ "$report" == *'state/nonproduction/example-thing'* && "$report" == *"\`branch-init\`"* \
        && "$report" == *'terraform init'* && "$report" == *'environments/production'* ]] \
        || fail "the branch failure summary omits the branch, classification, stage or root: $report"

    # An automation failure's stage and root are unreliable: the EXIT trap records the
    # literal stage `processing` and the first configured root whatever failed, and only
    # the provider lock policy records its real ones. Every processing diagnostic goes to
    # the processing step's log, so the summary names neither and points there instead.
    write_report_manifest "$manifest" automation \
        '{"stage": "processing", "root": "environments/production", "status": 1}'
    assert_silent_success 'reporting an automation failure' "$work/stdout" "$diagnostics" \
        run_report_step "$manifest" failure "$summary"
    report=$(<"$summary")
    [[ "$report" == *"\`automation\`"* ]] \
        || fail "the automation summary omits the classification: $report"
    [[ "$report" != *'environments/production'* && "$report" != *'Failed stage'* ]] \
        || fail "the automation summary claims a stage and root it cannot know: $report"
    local process_step
    process_step=$(yq -r '.jobs.process.steps[] | select(.id == "process") | .name' "$REUSABLE_WORKFLOW")
    [[ -n "$process_step" && "$report" == *"the \`$process_step\` step log names the cause"* ]] \
        || fail "the automation summary does not point at the processing step's log: $report"

    local classification
    for classification in success no-change; do
        write_report_manifest "$manifest" "$classification"
        assert_silent_success "reporting a $classification result" "$work/stdout" "$diagnostics" \
            run_report_step "$manifest" success "$summary"
        report=$(<"$summary")
        [[ "$report" == *"\`$classification\`"* ]] \
            || fail "the $classification summary omits the classification: $report"
    done

    # A branch failure that did not fail the processing step means the result is untrustworthy.
    write_report_manifest "$manifest" branch-validation \
        '{"stage": "terraform validate", "root": "environments/production", "status": 1}'
    ! run_report_step "$manifest" success "$summary" 2>"$diagnostics" \
        || fail 'reporting accepted a branch failure from a successful processing step'
    grep -qF 'the result contract is broken' "$diagnostics" \
        || fail "reporting did not report the broken contract: $(<"$diagnostics")"

    rm -f -- "$manifest"
    ! run_report_step "$manifest" failure "$summary" 2>"$diagnostics" \
        || fail 'reporting accepted a missing result manifest'
    grep -qF 'no result manifest' "$diagnostics" \
        || fail "reporting did not report the missing manifest: $(<"$diagnostics")"
}


test_workflow_summarises_update_logs() {
    local work="$TEST_TMP_ROOT/report-logs"
    rm -rf -- "$work"
    mkdir -p "$work/logs"
    local manifest="$work/result.json" summary="$work/summary.md" diagnostics="$work/stderr"
    local report
    write_report_manifest "$manifest" success null \
        '["environments/production", "environments/staging", "environments/missing"]'
    printf '%s\n' "Updated module source 'a/b/c' to version '1.2.3' in main.tf" '```' 'after the fence' \
        >"$work/logs/update-1.log"
    awk 'BEGIN { for (i = 1; i <= 1200; i++) printf "line %04d %060d\n", i, 0 }' \
        >"$work/logs/update-2.log"
    printf 'INIT-SENTINEL\n' >"$work/logs/init-1.log"
    printf 'VALIDATE-SENTINEL\n' >"$work/logs/validate-1.log"
    assert_silent_success 'summarising update logs' "$work/stdout" "$diagnostics" \
        run_report_step "$manifest" success "$summary"
    report=$(<"$summary")
    [[ "$report" == *'environments/production'* && "$report" == *'environments/staging'* ]] \
        || fail "the summary does not name each root that has an update log: $report"
    [[ "$report" != *'environments/missing'* ]] \
        || fail 'the summary names a root that has no update log'
    [[ "$report" == *"to version '1.2.3' in main.tf"* && "$report" == *'after the fence'* ]] \
        || fail "the summary omits the updater's output: $report"
    # A fence one backtick longer than any run in the log keeps a stray fence line inside it.
    [[ $(grep -cx '````' "$summary") -eq 2 ]] \
        || fail "the update log's fence does not outlast the backticks inside it: $report"
    # The truncated log ends mid-line, so dropping that partial line keeps its closing
    # fence on a line of its own.
    [[ "$report" == *$'\n```\n\nThis log was truncated'* ]] \
        || fail "the truncated update log's closing fence is not on a line of its own: $report"
    # Terraform's logs can carry a credential a provider echoed, so they stay in the artefact.
    [[ "$report" != *SENTINEL* ]] \
        || fail 'the summary exposes a Terraform log'
    grep -qF 'truncated' "$summary" \
        || fail 'an oversized update log was not marked as truncated'
    [[ $(wc -c <"$summary") -lt 80000 ]] \
        || fail 'an oversized update log was not truncated'

    # A NUL byte must not make grep treat the log as binary, which reports no backtick
    # run (GNU grep) or a "Binary file matches" line (BSD grep) instead of the longest.
    write_report_manifest "$manifest" success null '["environments/binary"]'
    printf 'before\0after\n````\n' >"$work/logs/update-1.log"
    assert_silent_success 'summarising an update log containing a NUL byte' "$work/stdout" "$diagnostics" \
        run_report_step "$manifest" success "$summary"
    [[ $(grep -acx '`````' "$summary") -eq 2 ]] \
        || fail 'a NUL byte hid the backticks inside an update log from its fence'
}


run_dry_run_report_step() {
    run_workflow_step publish 'Report dry-run outcome' "$2" RESULT_MANIFEST="$1"
}


test_workflow_reports_the_dry_run_outcome() {
    # Without a status function GitHub adds success(), so a failed dry-run preflight
    # reports nothing rather than a publication it would never have made. Following the
    # publish step, it reads the manifest from the directory publication checked.
    yq -o=json '.jobs.publish.steps' "$REUSABLE_WORKFLOW" | jq -e '
        (map(.name) | index("Publish processing result")) as $publish
        | (map(.name) | index("Report dry-run outcome")) as $report
        | (.[] | select(.name == "Publish processing result") | .env.RECONCILE_RESULT_DIR) as $result
        | [.[] | select(.name == "Report dry-run outcome")]
        | length == 1 and .[0].if == "${{ inputs.dry_run }}"
          and $publish != null and $report > $publish
          and .[0].env.RESULT_MANIFEST == $result + "/result.json"
    ' >/dev/null || fail 'the dry-run outcome is not reported only after a successful dry-run publication'
    local work="$TEST_TMP_ROOT/dry-run-report"
    rm -rf -- "$work"
    mkdir "$work"
    local manifest="$work/result.json" summary="$work/summary.md" diagnostics="$work/stderr"
    local classification expected
    for classification in success no-change branch-update branch-init branch-format branch-validation automation; do
        case "$classification" in
            success) expected='would push the update branch, create or refresh the pull request and close any failure issue, provided the state branch has not moved since discovery and any existing update branch belongs to this automation policy.' ;;
            no-change) expected='would close any open update pull request and failure issue, provided the state branch has not moved since discovery.' ;;
            branch-*) expected='would close any open update pull request and create or refresh the failure issue, provided the state branch has not moved since discovery.' ;;
            automation) expected='would not change any pull request, issue or ref.' ;;
        esac
        write_report_manifest "$manifest" "$classification"
        assert_silent_success "reporting a $classification dry run" "$work/stdout" "$diagnostics" \
            run_dry_run_report_step "$manifest" "$summary"
        grep -qF "$expected" "$summary" \
            || fail "the $classification dry run does not say what a live run would do: $(<"$summary")"
        grep -qF 'state/nonproduction/example-thing' "$summary" \
            || fail "the $classification dry run does not name its branch: $(<"$summary")"
    done
}


# Selecting the processing step by its id and requiring exactly one match keeps the
# guard honest: a renamed, reordered or removed step fails instead of matching nothing.
process_job_reports_processing_failures() {
    local workflow=$1
    yq -o=json '.jobs.process' "$workflow" | jq -e '
        (has("continue-on-error") | not) and
        ([.steps[] | select(.id == "process")] | length == 1 and (.[0] | has("continue-on-error") | not)) and
        ([.steps[] | select(.uses != null and (.uses | startswith("actions/upload-artifact")))
              | select(.if == "${{ always() }}")] | length == 1)
    ' >/dev/null
}


test_workflow_fails_the_process_job_on_processing_failure() {
    process_job_reports_processing_failures "$REUSABLE_WORKFLOW" \
        || fail 'the process job suppresses processing failures'
    local mutant="$TEST_TMP_ROOT/process-continue-on-error.yml"
    yq '(.jobs.process.steps[] | select(.id == "process"))."continue-on-error" = true' \
        "$REUSABLE_WORKFLOW" >"$mutant"
    ! process_job_reports_processing_failures "$mutant" \
        || fail 'the guard passes with continue-on-error on the processing step'
    local job_mutant="$TEST_TMP_ROOT/process-job-continue-on-error.yml"
    yq '.jobs.process."continue-on-error" = true' "$REUSABLE_WORKFLOW" >"$job_mutant"
    ! process_job_reports_processing_failures "$job_mutant" \
        || fail 'the guard passes with continue-on-error on the processing job'
}


# A failed process leg must neither cancel its sibling legs nor skip publication:
# every other branch still processes, and publish reconciles each discovered branch.
workflow_publishes_after_processing_failures() {
    local workflow=$1
    yq -o=json '.jobs' "$workflow" \
        | jq -e --arg condition "\${{ always() && needs.discover.result == 'success' }}" '
            .process.strategy["fail-fast"] == false and
            .publish.strategy["fail-fast"] == false and
            .publish.if == $condition
        ' >/dev/null
}


test_workflow_publishes_every_branch_after_processing_failures() {
    workflow_publishes_after_processing_failures "$REUSABLE_WORKFLOW" \
        || fail 'a processing failure cancels other branches or skips their publication'
    local mutant="$TEST_TMP_ROOT/process-fail-fast.yml"
    yq 'del(.jobs.process.strategy."fail-fast")' "$REUSABLE_WORKFLOW" >"$mutant"
    ! workflow_publishes_after_processing_failures "$mutant" \
        || fail 'the guard passes when a failed process leg cancels its siblings'
    mutant="$TEST_TMP_ROOT/publish-fail-fast.yml"
    yq 'del(.jobs.publish.strategy."fail-fast")' "$REUSABLE_WORKFLOW" >"$mutant"
    ! workflow_publishes_after_processing_failures "$mutant" \
        || fail 'the guard passes when a failed publish leg cancels its siblings'
    mutant="$TEST_TMP_ROOT/publish-condition.yml"
    yq 'del(.jobs.publish.if)' "$REUSABLE_WORKFLOW" >"$mutant"
    ! workflow_publishes_after_processing_failures "$mutant" \
        || fail 'the guard passes when publication runs only after every process leg succeeds'
}

test_workflow_offers_input_and_secret_terraform_environment() {
    yq -o=json '.on.workflow_call' "$REUSABLE_WORKFLOW" | jq -e '
        .inputs.terraform_env.type == "string" and
        .secrets.TERRAFORM_ENV.required == false
    ' >/dev/null || fail 'workflow does not offer both Terraform environment channels'
    # Masking redacts only console output written after it is registered, so it must
    # precede processing. Each channel carries its own source, never the other's.
    yq -o=json '.jobs.process.steps' "$REUSABLE_WORKFLOW" | jq -e '
        [.[] | select(.env.PROCESS_TERRAFORM_ENV != null and .env.PROCESS_TERRAFORM_SECRET_ENV != null)
             | (.env.PROCESS_TERRAFORM_ENV == "${{ inputs.terraform_env }}")
               and (.env.PROCESS_TERRAFORM_SECRET_ENV == "${{ secrets.TERRAFORM_ENV }}")
               and (.run | split("\n") | map(select(. != ""))
                    | (.[0] | endswith("mask")) and (.[1] | endswith("process")))] == [true]
    ' >/dev/null || fail 'the process job does not mask each supplied channel before processing'
    local caller
    for caller in production nonproduction; do
        yq -o=json '.jobs.automation.secrets.TERRAFORM_ENV' \
            "$SCRIPT_DIR/.github/workflows/tf-version-bump-$caller.yml" \
            | grep -qF 'secrets.TERRAFORM_ENV' \
            || fail "$caller caller does not forward the Terraform environment secret"
    done
}

cleanup_test_repositories() {
    cleanup_discovery_repository
    cleanup_processing_workspace
    cleanup_processing_container
    rm -rf -- "$TEST_TMP_ROOT"
}
trap cleanup_test_repositories EXIT

# Route subprocess Git through the caller's signer, including runtime publication tests.
if [[ "$TEST_GIT" != git ]]; then
    mkdir "$TEST_TMP_ROOT/git-bin"
    ln -s "$TEST_GIT" "$TEST_TMP_ROOT/git-bin/git"
    export PATH="$TEST_TMP_ROOT/git-bin:$PATH"
fi

if [[ $# -eq 0 ]]; then
    tests=(test_processing_container_setup_captures_pull_progress test_processing_combines_update_init_format_validate
        test_processing_updates_dependent_roots_before_init
        test_processing_validates_unchanged_candidates test_processing_init_upgrade_is_opt_in
        test_workflow_runs_three_jobs_with_current_attempt_results
        test_processing_records_real_update_and_format_failures test_processing_rejects_invalid_inputs_before_updates
        test_processing_invalid_config_reconciles_branch_failure
        test_processing_formats_only_after_dependency_or_lock_changes
        test_processing_rejects_ignored_generated_lock test_processing_rejects_formatter_changes_outside_patch_policy
        test_processing_rejects_files_created_during_the_run
        test_processing_supplies_terraform_environment_to_commands
        test_processing_delivers_escaped_github_app_credentials
        test_processing_scopes_supplied_environment_to_init_fmt_and_validate
        test_processing_masks_only_secret_environment_values
        test_processing_rejects_invalid_terraform_environment
        test_readme_documents_the_reserved_environment_names
        test_workflow_reports_the_processing_result
        test_workflow_fails_the_process_job_on_processing_failure
        test_workflow_publishes_every_branch_after_processing_failures
        test_workflow_offers_input_and_secret_terraform_environment
        test_workflow_summarises_update_logs test_workflow_reports_the_dry_run_outcome)
    while IFS= read -r test_name; do tests+=("$test_name"); done < <(compgen -A function test_discovery_)
    for test_name in "${tests[@]}"; do
        "$test_name"
        printf 'PASS: %s\n' "$test_name"
    done
    "$RECONCILE_TEST"
else
    for test_name in "$@"; do
        "$test_name"
        printf 'PASS: %s\n' "$test_name"
    done
fi
