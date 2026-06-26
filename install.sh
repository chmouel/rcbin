#!/bin/sh
set -eu

repo=${RC_REPO:-chmouel/rc}
tag=${RC_RELEASE_TAG:-nightly}
base_url="https://github.com/${repo}/releases/download/${tag}"

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

platform() {
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported operating system: $(uname -s)" ;;
	esac

	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

download() {
	curl -fsSL --retry 3 -o "$2" "$1"
}

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print tolower($1)}'
		return
	fi
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print tolower($1)}'
		return
	fi
	die "sha256sum or shasum is required"
}

install_dir() {
	if [ -n "${RC_INSTALL_DIR:-}" ]; then
		printf '%s\n' "$RC_INSTALL_DIR"
		return
	fi
	[ -n "${HOME:-}" ] || die "HOME is not set; set RC_INSTALL_DIR"
	printf '%s\n' "$HOME/.local/bin"
}

need awk
need curl
need find
need mktemp
need sed
need tar
platform

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/rc-install.XXXXXX")
tmp_target=
cleanup() {
	rm -rf "$tmp_dir"
	if [ -n "$tmp_target" ]; then
		rm -f "$tmp_target"
	fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

checksums="$tmp_dir/checksums.txt"
download "$base_url/checksums.txt" "$checksums"

suffix="_${os}_${arch}.tar.gz"
archive_name=$(
	awk -v suffix="$suffix" '
		{
			name = $NF
			if (length(name) >= length(suffix) &&
			    substr(name, length(name) - length(suffix) + 1) == suffix) {
				print name
				exit
			}
		}
	' "$checksums"
)
[ -n "$archive_name" ] || die "no ${os}/${arch} archive found in ${tag} release"

want=$(
	awk -v name="$archive_name" '
		$NF == name {
			print tolower($1)
			exit
		}
	' "$checksums"
)
[ -n "$want" ] || die "no checksum found for $archive_name"

archive="$tmp_dir/$archive_name"
download "$base_url/$archive_name" "$archive"

got=$(sha256_file "$archive")
[ "$got" = "$want" ] || die "checksum mismatch for $archive_name: got $got want $want"

extract_dir="$tmp_dir/extract"
mkdir -p "$extract_dir"
tar -xzf "$archive" -C "$extract_dir"
binary=$(
	find "$extract_dir" -type f -name rc -print | sed -n '1p'
)
[ -n "$binary" ] || die "$archive_name does not contain an rc binary"

dest_dir=$(install_dir)
[ -n "$dest_dir" ] || die "install directory is empty"
[ "$dest_dir" != "/" ] || die "refusing to install into /"
mkdir -p "$dest_dir"

target="$dest_dir/rc"
[ ! -d "$target" ] || die "$target is a directory"
tmp_target="$dest_dir/.rc.tmp.$$"
cp "$binary" "$tmp_target"
chmod 0755 "$tmp_target"
mv "$tmp_target" "$target"
tmp_target=

printf 'Installed rc from %s to %s\n' "$archive_name" "$target"
