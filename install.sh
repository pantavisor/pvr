#!/bin/sh
# shellcheck shell=sh
set -eu

# Install pvr from GitLab Packages
# Usage:
#   curl -fsSL https://gitlab.com/pantacor/pvr/-/raw/master/install.sh | bash
#   curl -fsSL https://gitlab.com/pantacor/pvr/-/raw/master/install.sh | PVR_CHANNEL=develop bash
#   curl -fsSL https://gitlab.com/pantacor/pvr/-/raw/master/install.sh | PVR_VERSION=046 bash
#
# Environment variables:
#   PVR_VERSION     - Specific version to install (default: latest)
#   PVR_CHANNEL     - Channel to fetch version from: stable or develop (default: stable)
#   PVR_INSTALL_DIR - Installation directory (default: $HOME/.local/bin)
#   GITLAB_HOST     - GitLab instance (default: gitlab.com)

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Default values
VERSION="${PVR_VERSION:-latest}"
CHANNEL="${PVR_CHANNEL:-stable}"
GITLAB_HOST="${GITLAB_HOST:-gitlab.com}"
PROJECT_ID="${PVR_PROJECT_ID:-pantacor%2Fpvr}"
INSTALL_DIR="${PVR_INSTALL_DIR:-${HOME}/.local/bin}"

info() {
	printf "${GREEN}[INFO]${NC} %s\n" "$1"
}

warn() {
	printf "${YELLOW}[WARN]${NC} %s\n" "$1"
}

error() {
	printf "${RED}[ERROR]${NC} %s\n" "$1"
	exit 1
}

check_dependencies() {
	for cmd in curl tar gzip; do
		if ! command -v "$cmd" >/dev/null 2>&1; then
			error "Required command '$cmd' not found. Please install it first."
		fi
	done
}

detect_os() {
	os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
	case "$os_name" in
	linux | darwin)
		printf '%s' "$os_name"
		;;
	msys* | mingw* | cygwin*)
		printf '%s' "windows"
		;;
	*)
		error "Unsupported OS: $os_name"
		;;
	esac
}

detect_arch() {
	arch_name="$(uname -m)"
	case "$arch_name" in
	x86_64 | amd64)
		printf '%s' "amd64"
		;;
	aarch64 | arm64)
		printf '%s' "arm64"
		;;
	armv7l | armv6l | arm)
		printf '%s' "arm"
		;;
	i386 | i686)
		printf '%s' "x32"
		;;
	*)
		error "Unsupported architecture: $arch_name"
		;;
	esac
}

get_latest_version() {
	if [ "$CHANNEL" = "stable" ]; then
		version="$(curl -s "https://${GITLAB_HOST}/api/v4/projects/${PROJECT_ID}/packages?per_page=100&order_by=created_at&sort=desc" |
			grep -o '"version":"[^"]*"' |
			grep -v 'develop' |
			head -1 | cut -d'"' -f4)"
	else
		version="$(curl -s "https://${GITLAB_HOST}/api/v4/projects/${PROJECT_ID}/packages?per_page=1&order_by=created_at&sort=desc" |
			grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4)"
	fi

	if [ -z "$version" ]; then
		error "Failed to fetch version from GitLab"
	fi

	printf '%s' "$version"
}

download_pvr() {
	version="$1"
	os="$2"
	arch="$3"

	if [ "$os" = "windows" ]; then
		if [ "$arch" = "amd64" ]; then
			filename="pvr.${version}.windows.x64.zip"
			extract_cmd="unzip"
		else
			filename="pvr.${version}.windows.x32.zip"
			extract_cmd="unzip"
		fi
	else
		filename="pvr.${version}.${os}.${arch}.gz"
		extract_cmd="tar -xzf"
	fi

	url="https://${GITLAB_HOST}/api/v4/projects/${PROJECT_ID}/packages/generic/pvr/${version}/${filename}"

	info "Downloading pvr ${version} for ${os}/${arch}..."
	info "URL: $url"

	tmp_dir="$(mktemp -d)"
	download_file="${tmp_dir}/${filename}"

	if ! curl -fsSL "$url" -o "$download_file"; then
		error "Failed to download pvr from $url"
	fi

	cd "${tmp_dir}"
	if [ "$extract_cmd" = "unzip" ]; then
		if ! command -v unzip >/dev/null 2>&1; then
			error "unzip is required for Windows. Please install it first."
		fi
		unzip -q "$download_file"
	else
		tar -xzf "$download_file"
	fi

	if [ ! -f "${tmp_dir}/pvr" ] && [ ! -f "${tmp_dir}/pvr.exe" ]; then
		error "Failed to extract pvr binary"
	fi

	mkdir -p "$INSTALL_DIR"

	if [ -f "${tmp_dir}/pvr" ]; then
		mv "${tmp_dir}/pvr" "$INSTALL_DIR/"
		chmod +x "$INSTALL_DIR/pvr"
	else
		mv "${tmp_dir}/pvr.exe" "$INSTALL_DIR/"
	fi

	rm -rf "$tmp_dir"
}

main() {
	check_dependencies

	os_name="$(detect_os)"
	arch_name="$(detect_arch)"

	info "Detected OS: $os_name"
	info "Detected Arch: $arch_name"
	info "Channel: $CHANNEL"

	if [ "$VERSION" = "latest" ]; then
		VERSION="$(get_latest_version)"
		info "Latest version: $VERSION"
	fi

	download_pvr "$VERSION" "$os_name" "$arch_name"

	if [ "$os_name" != "windows" ]; then
		info "Successfully installed pvr to $INSTALL_DIR/pvr"
		info ""
		info "Add $INSTALL_DIR to your PATH or run:"
		info "  export PATH=\"\$PATH:$INSTALL_DIR\""
	else
		info "Successfully installed pvr to $INSTALL_DIR/pvr.exe"
		info ""
		info "Add $INSTALL_DIR to your PATH"
	fi
}

main
