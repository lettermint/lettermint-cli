#!/bin/sh
# Lettermint CLI installer for macOS and Linux. No root access is required.
# The release workflow sets this public ID before it calculates checksums.
EXPECTED_MACOS_TEAM_ID='REPLACE_WITH_APPLE_TEAM_ID'

usage() {
    cat <<'EOF'
Install the latest stable Lettermint CLI:
  sh install.sh

Options:
  --version v1.0.0       Install an exact version, including a pre-release.
  --bin-dir DIRECTORY   Use this directory. Default: $HOME/.local/bin
  --allow-downgrade     Permit an older version.
  --verify-provenance   Also verify GitHub build provenance. Requires gh.
  --uninstall           Remove an installation made by this script.
  --help                Show this help.

The script checks SHA-256 checksums. On macOS it also checks the Apple
publisher and notarization. Saved profiles and agent settings are kept.
Use Homebrew or PowerShell to update installations owned by those tools.
EOF
}

fail() { printf 'Lettermint: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "Required command is missing: $1"; }

valid_version() {
    LC_ALL=C awk -v value="$1" 'BEGIN {
        if (value !~ /^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$/) exit 1
        sub(/^v/, "", value)
        core = value; sub(/-.*/, "", core)
        split(core, parts, ".")
        for (i = 1; i <= 3; i++) if (parts[i] ~ /^0[0-9]/) exit 1
        if (value ~ /-/) {
            sub(/^[^-]*-/, "", value)
            count = split(value, parts, ".")
            for (i = 1; i <= count; i++)
                if (parts[i] == "" || parts[i] ~ /^0[0-9]+$/) exit 1
        }
    }'
}

is_older() {
    LC_ALL=C awk -v wanted="${1#v}" -v installed="${2#v}" '
    function number(a, b) {
        if (length(a) != length(b)) return length(a) < length(b) ? -1 : 1
        return ("x" a == "x" b) ? 0 : (("x" a < "x" b) ? -1 : 1)
    }
    BEGIN {
        wc = wanted; sub(/-.*/, "", wc)
        ic = installed; sub(/-.*/, "", ic)
        split(wc, w, "."); split(ic, v, ".")
        for (i = 1; i <= 3; i++) {
            c = number(w[i], v[i]); if (c) exit c < 0 ? 0 : 1
        }
        wp = wanted ~ /-/; ip = installed ~ /-/
        if (wp != ip) exit wp ? 0 : 1
        if (!wp) exit 1
        sub(/^[^-]*-/, "", wanted); sub(/^[^-]*-/, "", installed)
        wn = split(wanted, w, "."); vn = split(installed, v, ".")
        for (i = 1; i <= wn && i <= vn; i++) {
            if ("x" w[i] == "x" v[i]) continue
            a = w[i] ~ /^[0-9]+$/; b = v[i] ~ /^[0-9]+$/
            if (a && b) exit number(w[i], v[i]) < 0 ? 0 : 1
            if (a != b) exit a ? 0 : 1
            exit ("x" w[i] < "x" v[i]) ? 0 : 1
        }
        exit wn < vn ? 0 : 1
    }'
}

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        hash_output=$(sha256sum "$1") || return 1
    else
        hash_output=$(shasum -a 256 "$1") || return 1
    fi
    printf '%s\n' "$hash_output" | awk '{
        if (length($1) != 64 || $1 ~ /[^0-9a-f]/) exit 1
        print $1
    }'
}

download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --tlsv1.2 --connect-timeout 15 --max-time 180 --max-filesize 104857600 \
        --retry 3 --output "$2" "$1"
}

cleanup() {
    result=$?
    trap - 0
    set +e
    if [ "$changing" = 1 ]; then
        restore_failed=0
        if [ "$owned" = 1 ]; then
            mv -f "$stage_dir/previous-binary" "$target" || restore_failed=1
            mv -f "$stage_dir/previous-marker" "$marker" || restore_failed=1
        else
            rm -f "$target" "$marker" || restore_failed=1
        fi
        if [ "$restore_failed" = 1 ]; then
            printf 'Lettermint: Restore failed. Keep the backup at %s for recovery.\n' "$stage_dir" >&2
            stage_dir=''
        fi
    fi
    [ -z "$stage_dir" ] || rm -rf "$stage_dir"
    [ -z "$workspace" ] || rm -rf "$workspace"
    [ "$lock_owned" = 0 ] || rmdir "$lock_dir"
    exit "$result"
}

main() (
    set -eu
    umask 077
    requested=''; bin_dir="${HOME:?HOME must be set}/.local/bin"
    downgrade=0; provenance=0; remove=0
    workspace=''; stage_dir=''; changing=0; owned=0; lock_owned=0
    trap cleanup 0
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --version) [ "$#" -ge 2 ] || fail 'Provide a version.'; requested=$2; shift 2 ;;
            --bin-dir) [ "$#" -ge 2 ] && [ -n "$2" ] || fail 'Provide a directory.'; bin_dir=$2; shift 2 ;;
            --allow-downgrade) downgrade=1; shift ;;
            --verify-provenance) provenance=1; shift ;;
            --uninstall) remove=1; shift ;;
            --help|-h) usage; exit 0 ;;
            *) fail 'Unknown option. Use --help.' ;;
        esac
    done
    need awk
    if [ -n "$requested" ]; then valid_version "$requested" || fail 'Use a version such as v1.0.0 or v1.0.0-rc.1.'; fi
    if [ "$remove" = 1 ]; then
        [ -z "$requested" ] && [ "$downgrade" = 0 ] && [ "$provenance" = 0 ] || fail '--uninstall cannot be combined with install options.'
        [ -d "$bin_dir" ] || fail 'No shell-managed installation was found.'
    else
        need curl; need tar; need uname
        [ "$provenance" = 0 ] || need gh
    fi
    command -v sha256sum >/dev/null 2>&1 || need shasum
    case "$bin_dir" in /*) ;; *) bin_dir="$PWD/$bin_dir" ;; esac
    mkdir -p "$bin_dir"
    bin_dir=$(CDPATH='' cd "$bin_dir" && pwd -P)
    target="$bin_dir/lettermint"; marker="$bin_dir/.lettermint-install"
    lock_dir="$bin_dir/.lettermint-install.lock"
    mkdir "$lock_dir" 2>/dev/null || fail "Another install is active, or a stale lock exists: $lock_dir"
    lock_owned=1
    [ ! -L "$target" ] && [ ! -L "$marker" ] || fail 'An existing symbolic link belongs to another installation. Use its package manager.'
    if [ -e "$target" ] || [ -e "$marker" ]; then
        [ -f "$target" ] && [ -f "$marker" ] || fail 'The existing installation has no valid ownership record. Keep it or remove it manually.'
        [ "$(sed -n '1p' "$marker")" = 'lettermint-shell-v1' ] || fail 'Another installer owns this executable.'
        previous=$(sed -n '2p' "$marker")
        valid_version "$previous" || fail 'The ownership record has an invalid version.'
        installed_hash=$(sha256 "$target") || fail 'Cannot check the installed executable.'
        [ "$installed_hash" = "$(sed -n '3p' "$marker")" ] || fail 'The installed executable changed. Review it before replacement or removal.'
        owned=1
    fi
    if [ "$remove" = 0 ]; then
        visible=$(command -v lettermint 2>/dev/null || true)
        if [ -n "$visible" ]; then
            case "$visible" in */*) visible_dir=$(CDPATH='' cd "$(dirname "$visible")" && pwd -P) ;; *) fail 'A shell command already uses the name lettermint.' ;; esac
            [ "$visible_dir/lettermint" = "$target" ] || fail "Lettermint is already installed at $visible. Use that installer to update it."
        fi
    fi

    if [ "$remove" = 1 ]; then
        [ "$owned" = 1 ] || fail 'No shell-managed installation was found.'
    else
        case "$(uname -s)" in
            Darwin) system=darwin ;;
            Linux) system=linux ;;
            *) fail 'This script supports macOS and Linux. On Windows, use the signed PowerShell installer.' ;;
        esac
        case "$(uname -m)" in
            x86_64|amd64) arch=amd64 ;;
            arm64|aarch64) arch=arm64 ;;
            *) fail 'This CPU architecture is not supported.' ;;
        esac
        if [ "$system" = darwin ]; then
            need codesign
            if [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then arch=arm64; fi
            printf '%s\n' "$EXPECTED_MACOS_TEAM_ID" | LC_ALL=C grep -Eq '^[A-Z0-9]{10}$' || fail 'Use install.sh from a Lettermint release. The source template has no Apple team ID.'
        fi
        task_tmp_parent=${TMPDIR:-/tmp}
        case "$task_tmp_parent" in /*) ;; *) fail 'TMPDIR must be an absolute path.' ;; esac
        workspace=$(mktemp -d "$task_tmp_parent/lettermint.XXXXXXXX")
        base='https://github.com/lettermint/lettermint-cli/releases'
        if [ -z "$requested" ]; then
            latest=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
                --tlsv1.2 --connect-timeout 15 --max-time 60 --retry 3 --output /dev/null \
                --write-out '%{url_effective}' "$base/latest") || fail 'No stable release is available. Try again after the first stable release.'
            case "$latest" in "$base/tag/"*) requested=${latest#"$base/tag/"} ;; *) fail 'The latest-release URL is invalid.' ;; esac
            valid_version "$requested" || fail 'The latest-release version is invalid.'
            case "$requested" in *-*) fail 'The latest release must be stable. Select a pre-release with --version.' ;; esac
        fi
        if [ "$owned" = 1 ] && [ "$downgrade" = 0 ] && is_older "$requested" "$previous"; then
            fail 'Use --allow-downgrade to install an older version.'
        fi
        archive="lettermint_${requested#v}_${system}_${arch}.tar.gz"
        printf 'Downloading Lettermint %s for %s/%s...\n' "$requested" "$system" "$arch" >&2
        download "$base/download/$requested/$archive" "$workspace/$archive" || fail 'The release archive is unavailable. Wait for the release workflow to finish, then retry.'
        download "$base/download/$requested/checksums.txt" "$workspace/checksums.txt" || fail 'The release checksum file is unavailable.'
        expected=$(awk -v name="$archive" '$2 == name { count++; value=$1 } END {
            if (count != 1 || length(value) != 64 || value ~ /[^0-9a-f]/) exit 1
            print value
        }' "$workspace/checksums.txt") || fail 'The archive checksum is missing or ambiguous.'
        archive_hash=$(sha256 "$workspace/$archive") || fail 'Cannot check the release archive.'
        [ "$archive_hash" = "$expected" ] || fail 'The archive checksum does not match.'
        if [ "$provenance" = 1 ]; then
            download "$base/download/$requested/provenance.jsonl" "$workspace/provenance.jsonl" || fail 'Release provenance is unavailable.'
            gh attestation verify "$workspace/$archive" --repo lettermint/lettermint-cli \
                --bundle "$workspace/provenance.jsonl" \
                --signer-workflow lettermint/lettermint-cli/.github/workflows/release.yml \
                --source-ref "refs/tags/$requested" || fail 'Build provenance could not be verified.'
        fi
        # Extract only the named executable to stdout; no archive paths are written.
        tar -tvzf "$workspace/$archive" lettermint > "$workspace/member.txt" || fail 'The archive has no CLI executable.'
        awk 'NR != 1 || substr($0,1,1) != "-" { exit 1 } END { if (NR != 1) exit 1 }' "$workspace/member.txt" || fail 'The archive must contain one regular CLI executable.'
        tar -xzOf "$workspace/$archive" lettermint > "$workspace/lettermint" || fail 'Cannot extract the CLI executable.'
        chmod 755 "$workspace/lettermint"
        if [ "$system" = darwin ]; then
            codesign --verify --strict "$workspace/lettermint" || fail 'The macOS code signature is invalid.'
            codesign --display --verbose=4 "$workspace/lettermint" > "$workspace/identity.txt" 2>&1 || fail 'Cannot read the macOS publisher.'
            grep -Fxq "TeamIdentifier=$EXPECTED_MACOS_TEAM_ID" "$workspace/identity.txt" || fail 'The macOS publisher does not match Lettermint.'
            codesign --verify --strict -R=notarized --check-notarization "$workspace/lettermint" || fail 'macOS notarization could not be verified.'
        fi
        reported=$("$workspace/lettermint" version --json --no-input) || fail 'The downloaded CLI cannot run on this computer.'
        actual=$(printf '%s\n' "$reported" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
        [ "$actual" = "${requested#v}" ] || fail 'The executable version does not match the release.'
    fi

    stage_dir=$(mktemp -d "$bin_dir/.lettermint-stage.XXXXXXXX")
    if [ "$owned" = 1 ]; then
        cp -p "$target" "$stage_dir/previous-binary"
        cp -p "$marker" "$stage_dir/previous-marker"
    fi
    if [ "$remove" = 1 ]; then
        changing=1
        rm "$target" "$marker"
        changing=0
        printf 'Lettermint CLI removed. Saved profiles remain.\n'
    else
        cp "$workspace/lettermint" "$stage_dir/lettermint"
        chmod 755 "$stage_dir/lettermint"
        executable_hash=$(sha256 "$stage_dir/lettermint") || fail 'Cannot check the new executable.'
        printf 'lettermint-shell-v1\n%s\n%s\n' "$requested" "$executable_hash" > "$stage_dir/marker"
        changing=1
        mv -f "$stage_dir/lettermint" "$target"
        mv -f "$stage_dir/marker" "$marker"
        changing=0
        printf 'Installed Lettermint %s at %s\n' "$requested" "$target"
        case ":${PATH:-}:" in *":$bin_dir:"*) ;; *) printf 'Add this directory to PATH: %s\n' "$bin_dir" ;; esac
    fi
)

main "$@"
