#!/bin/sh
# Installs or updates kijk on macOS and Linux:
#   curl -fsSL https://github.com/kijk-dev/kijk/releases/latest/download/install.sh | sh
# KIJK_INSTALL_DIR picks the folder. It never uses sudo and never edits shell files.
set -eu

releases="${KIJK_RELEASES_URL:-https://github.com/kijk-dev/kijk/releases}"
releases="${releases%/}"

say() { printf '%s\n' "$*"; }
fail() { printf 'kijk install: %s\n' "$*" >&2; exit 1; }

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 2 -o "$2" "$1" || fail "could not download $1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1" || fail "could not download $1"
  else
    fail "needs curl or wget"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

on_path() {
  case ":$PATH:" in *":$1:"*) return 0 ;; esac
  return 1
}

pick_dir() {
  if [ -n "${KIJK_INSTALL_DIR:-}" ]; then
    mkdir -p "$KIJK_INSTALL_DIR" 2>/dev/null && [ -w "$KIJK_INSTALL_DIR" ] || fail "cannot write to $KIJK_INSTALL_DIR"
    printf '%s' "$KIJK_INSTALL_DIR"
    return
  fi
  for d in "$HOME/.local/bin" "$HOME/bin" /usr/local/bin; do
    if on_path "$d" && [ -d "$d" ] && [ -w "$d" ]; then
      printf '%s' "$d"
      return
    fi
  done
  mkdir -p "$HOME/.local/bin" || fail "cannot create $HOME/.local/bin"
  printf '%s' "$HOME/.local/bin"
}

path_hint() {
  say ""
  say "$1 is not on your PATH. To add it, run:"
  case "$(basename "${SHELL:-sh}")" in
    fish) say "  fish_add_path $1" ;;
    zsh) say "  echo 'export PATH=\"$1:\$PATH\"' >> ~/.zshrc" ;;
    bash) say "  echo 'export PATH=\"$1:\$PATH\"' >> ~/.bashrc" ;;
    *) say "  export PATH=\"$1:\$PATH\"   (and add that line to your shell's start-up file)" ;;
  esac
  say "Then open a new terminal and run kijk."
}

main() {
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "kijk has no binary for $(uname -s). On Windows, use install.ps1." ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "kijk has no binary for $os on $(uname -m)" ;;
  esac
  # A shell under Rosetta reports x86_64 on an Apple Silicon Mac.
  if [ "$os" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
    arch=arm64
  fi
  name="kijk-$os-$arch"

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  say "Downloading $name"
  fetch "$releases/latest/download/checksums.txt" "$tmp/checksums.txt"
  fetch "$releases/latest/download/$name" "$tmp/kijk"
  want=$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1 }' "$tmp/checksums.txt")
  [ -n "$want" ] || fail "checksums.txt has no line for $name. Nothing was installed."
  [ "$(printf '%s\n' "$want" | wc -l | tr -d ' ')" = 1 ] || fail "checksums.txt lists $name twice. Nothing was installed."
  [ "$(sha256 "$tmp/kijk")" = "$want" ] || fail "$name does not match checksums.txt. Nothing was installed."
  chmod 755 "$tmp/kijk"
  new=$("$tmp/kijk" version 2>/dev/null) || fail "the downloaded kijk does not run on this machine"
  new=${new#kijk }

  dir=$(pick_dir)
  old=""
  if [ -x "$dir/kijk" ]; then
    old=$("$dir/kijk" -version 2>/dev/null || true)
    old=${old#kijk }
  fi
  # Copy next to the target first, so the final step is a rename within one folder.
  if ! { cp "$tmp/kijk" "$dir/.kijk-install" && mv -f "$dir/.kijk-install" "$dir/kijk"; }; then
    rm -f "$dir/.kijk-install"
    fail "cannot write to $dir"
  fi

  if [ -n "$old" ] && [ "$old" != "$new" ]; then
    say "kijk $old → $new in $dir"
  else
    say "kijk $new is installed in $dir"
  fi
  if on_path "$dir"; then
    say "Run kijk to start it."
  else
    path_hint "$dir"
  fi
}

main "$@"
