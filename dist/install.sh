#!/bin/sh
# corenexd one-command installer.
#
# Installs the telemetry binary to /usr/local/bin, the udev rule (so the
# desktop user can drive the display without root) and the systemd service,
# then starts the service — or restarts it when upgrading in place.
#
# Usage:
#   sudo ./dist/install.sh                 install using the files in dist/
#   sudo REPO=owner/name ./install.sh      download the binary from a fork's
#                                          GitHub releases instead
#   sudo ./dist/install.sh uninstall       remove everything installed here
#
# The piped form installs everything too — the binary comes from the repo's
# latest GitHub release, the udev rule and service unit from the repo's
# default branch (override with BRANCH=...):
#   curl -fsSL .../dist/install.sh | sudo sh
set -eu

# All logging goes to stderr: functions whose stdout is captured by the
# caller (dist_file) must not print anything else to stdout.
log() { printf 'install: %s\n' "$*" >&2; }
die() { printf 'install: error: %s\n' "$*" >&2; exit 1; }

# Where to download from when dist/ is not next to this script
# (i.e. when piped from the README one-liner).
REPO="${REPO:-duckyman-ai/corenexd}"
BRANCH="${BRANCH:-main}"

[ "$(id -u)" -eq 0 ] || exec sudo env REPO="$REPO" BRANCH="$BRANCH" sh "$0" "$@"

bin=/usr/local/bin/corenexd
unit=/etc/systemd/system/corenexd.service
rule=/etc/udev/rules.d/60-corenexd.rules
here=$(cd "$(dirname "$0")" && pwd)
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

if [ "${1:-}" = "uninstall" ]; then
    log "removing corenexd"
    command -v systemctl >/dev/null 2>&1 && systemctl disable --now corenexd 2>/dev/null || true
    rm -f "$bin" "$unit" "$rule"
    command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload || true
    command -v udevadm  >/dev/null 2>&1 && udevadm control --reload   || true
    log "removed $bin"
    log "removed $unit"
    log "removed $rule"
    exit 0
fi

[ "$(uname -s)" = "Linux" ] || die "this installer and corenexd are Linux-only"

case "$(uname -m)" in
    x86_64)  arch=amd64 ;;
    aarch64) arch=arm64 ;;
    *)       die "unsupported architecture $(uname -m) — build from source instead" ;;
esac

# A running service keeps executing the old binary after the file is
# replaced, so remember whether it was up — the service is restarted at
# the end to pick up the new binary.
was_active=0
if command -v systemctl >/dev/null 2>&1; then
    systemctl is-active --quiet corenexd && was_active=1 || true
fi

src="$here/corenexd-linux-$arch"
if [ ! -f "$src" ]; then
    command -v curl >/dev/null 2>&1 || die "curl is required to download from GitHub"
    url="https://github.com/$REPO/releases/latest/download/corenexd-linux-$arch"
    log "no local dist file — downloading $url"
    curl -fsSL "$url" -o "$tmpdir/corenexd-linux-$arch" \
        || die "download failed — does $REPO publish corenexd-linux-$arch as a release asset?"
    src="$tmpdir/corenexd-linux-$arch"
fi

log "installing binary -> $bin"
install -m 755 "$src" "$bin"

# dist_file NAME — echo a path to a usable copy of dist/NAME: the one next
# to this script when running from a clone, downloaded from the repo
# otherwise (piped installs have no local dist).
dist_file() {
    if [ -f "$here/$1" ]; then
        printf '%s\n' "$here/$1"
        return 0
    fi
    command -v curl >/dev/null 2>&1 || die "curl is required to download from GitHub"
    log "no local dist file — downloading dist/$1"
    curl -fsSL "https://raw.githubusercontent.com/$REPO/$BRANCH/dist/$1" \
        -o "$tmpdir/$1" || die "download failed — is dist/$1 on $REPO's $BRANCH branch?"
    printf '%s\n' "$tmpdir/$1"
}

log "installing udev rule -> $rule"
install -m 644 "$(dist_file 60-corenexd.rules)" "$rule"
command -v udevadm >/dev/null 2>&1 && udevadm control --reload && udevadm trigger --subsystem-match=hidraw || true

log "installing systemd service -> $unit"
install -m 644 "$(dist_file corenexd.service)" "$unit"
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
    if [ "$was_active" = 1 ]; then
        systemctl restart corenexd
        log "restarted corenexd — the new binary is now in use"
    else
        systemctl enable --now corenexd || log "WARNING: service did not start — run 'corenexd --doctor'"
        log "service enabled and started"
    fi
else
    log "systemctl not found — service file installed but not started; run corenexd manually"
fi

log "done — the display should come alive within a few seconds"
log "check status: systemctl status corenexd --no-pager"
log "diagnostics:  corenexd --doctor"
