#!/bin/sh
# corenexd one-command installer.
#
# Installs the telemetry binary to /usr/local/bin, the udev rule (so the
# desktop user can drive the display without root) and the systemd service,
# then starts the service.
#
# Usage:
#   sudo ./dist/install.sh                 install using the files in dist/
#   sudo REPO=owner/name ./install.sh      download the binary from a fork's
#                                          GitHub releases instead
#   sudo ./dist/install.sh uninstall       remove everything installed here
#
# The piped form works too:
#   curl -fsSL .../dist/install.sh | sudo sh
set -eu

log() { printf 'install: %s\n' "$*"; }
die() { printf 'install: error: %s\n' "$*" >&2; exit 1; }

# Where to download the binary from when dist/ is not next to this script
# (i.e. when piped from the README one-liner).
REPO="${REPO:-duckyman-ai/corenexd}"

[ "$(id -u)" -eq 0 ] || exec sudo env REPO="${REPO:-}" sh "$0" "$@"

bin=/usr/local/bin/corenexd
unit=/etc/systemd/system/corenexd.service
rule=/etc/udev/rules.d/60-corenexd.rules
here=$(cd "$(dirname "$0")" && pwd)

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

src="$here/corenexd-linux-$arch"
if [ ! -f "$src" ]; then
    command -v curl >/dev/null 2>&1 || die "curl is required to download from GitHub"
    url="https://github.com/$REPO/releases/latest/download/corenexd-linux-$arch"
    log "no local dist file — downloading $url"
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT
    curl -fsSL "$url" -o "$tmp" || die "download failed — does $REPO publish corenexd-linux-$arch as a release asset?"
    src="$tmp"
fi

log "installing binary -> $bin"
install -m 755 "$src" "$bin"

if [ -f "$here/60-corenexd.rules" ]; then
    log "installing udev rule -> $rule"
    install -m 644 "$here/60-corenexd.rules" "$rule"
    command -v udevadm >/dev/null 2>&1 && udevadm control --reload && udevadm trigger --subsystem-match=hidraw || true
fi

if [ -f "$here/corenexd.service" ]; then
    log "installing systemd service -> $unit"
    install -m 644 "$here/corenexd.service" "$unit"
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload
        systemctl enable --now corenexd || log "WARNING: service did not start — run 'corenexd --doctor'"
        log "service enabled and started"
    else
        log "systemctl not found — service file installed but not started; run corenexd manually"
    fi
fi

log "done — the display should come alive within a few seconds"
log "check status: systemctl status corenexd --no-pager"
log "diagnostics:  corenexd --doctor"
