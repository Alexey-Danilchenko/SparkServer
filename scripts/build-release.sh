#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
DIST_DIR=${DIST_DIR:-dist}
PLATFORMS=${PLATFORMS:-"darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 linux/arm/6 windows/amd64 windows/arm64"}
COMMANDS="spark-server sparkctl"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

for platform in $PLATFORMS; do
	old_ifs=$IFS
	IFS=/
	set -- $platform
	IFS=$old_ifs

	GOOS=${1:-}
	GOARCH=${2:-}
	GOARM=${3:-}

	if [ -z "$GOOS" ] || [ -z "$GOARCH" ] || [ -n "${4:-}" ]; then
		echo "invalid platform target: $platform" >&2
		exit 1
	fi

	package_name="spark-server_${VERSION}_${GOOS}_${GOARCH}"
	if [ -n "$GOARM" ]; then
		package_name="${package_name}v${GOARM}"
	fi
	package_dir="$DIST_DIR/$package_name"
	suffix=""

	if [ "$GOOS" = "windows" ]; then
		suffix=".exe"
	fi

	mkdir -p "$package_dir"

	for command in $COMMANDS; do
		if [ -n "$GOARM" ]; then
			CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" GOARM="$GOARM" go build \
				-trimpath \
				-ldflags="-s -w" \
				-o "$package_dir/$command$suffix" \
				"./cmd/$command"
		else
			CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
				-trimpath \
				-ldflags="-s -w" \
				-o "$package_dir/$command$suffix" \
				"./cmd/$command"
		fi
	done

	cp README.md COMPATIBILITY.md RELEASE.md "$package_dir/"
	cp -R examples "$package_dir/"

	(
		cd "$DIST_DIR"
		tar -czf "$package_name.tar.gz" "$package_name"
		rm -rf "$package_name"
	)
done

(
	cd "$DIST_DIR"
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 ./*.tar.gz > checksums.txt
	elif command -v sha256sum >/dev/null 2>&1; then
		sha256sum ./*.tar.gz > checksums.txt
	else
		echo "warning: neither shasum nor sha256sum found; skipping checksums" >&2
	fi
)
