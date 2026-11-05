#!/usr/bin/env bash
# Builds whisper.cpp's whisper-cli from source (a pinned release) for retake's transcription. It uses CUDA when nvcc is
# found and the CPU otherwise. Models aren't downloaded here: retake fetches them on first use.
#
# usage: scripts/install-whisper.sh [--cpu | --cuda] [--prefix DIR] [--jobs N] [--keep-build]
#   --cpu         build for the CPU even when nvcc is found
#   --cuda        build with CUDA, or fail if there's no nvcc
#   --prefix DIR  install into DIR (default: $XDG_DATA_HOME/retake/whisper.cpp, i.e. ~/.local/share/retake/whisper.cpp)
#   --jobs N      parallel compile jobs (default: the number of CPUs)
#   --keep-build  keep the source and build folders (in $XDG_CACHE_HOME/retake/build/)
# env: CUDACXX or CUDA_HOME point at a CUDA toolkit that isn't on $PATH. CUDAFLAGS adds nvcc flags, e.g.
#      CUDAFLAGS=-allow-unsupported-compiler when the host gcc is newer than the toolkit knows.
#
# The result is <prefix>/bin/whisper-cli, one self-contained binary (plus the CUDA runtime libraries for a CUDA
# build), and <prefix>/BUILD, which records the release and the backend. retake finds it there when whisper-cli isn't
# on $PATH and [transcribe] whisper_cli isn't set.
set -euo pipefail

TAG=v1.9.5
SHA256=ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720
URL="https://github.com/ggml-org/whisper.cpp/archive/refs/tags/$TAG.tar.gz"

die() { echo "install-whisper: $*" >&2; exit 1; }
say() { echo "install-whisper: $*"; }

mode=auto
prefix="${XDG_DATA_HOME:-$HOME/.local/share}/retake/whisper.cpp"
jobs=""
keep=0
while [ $# -gt 0 ]; do
  case "$1" in
    --cpu) mode=cpu ;;
    --cuda) mode=cuda ;;
    --prefix) [ $# -ge 2 ] || die "--prefix needs a folder"; prefix="$2"; shift ;;
    --prefix=*) prefix="${1#--prefix=}" ;;
    --jobs) [ $# -ge 2 ] || die "--jobs needs a number"; jobs="$2"; shift ;;
    --jobs=*) jobs="${1#--jobs=}" ;;
    --keep-build) keep=1 ;;
    -h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option $1 (see --help)" ;;
  esac
  shift
done
case "$prefix" in /*) ;; *) prefix="$PWD/$prefix" ;; esac

# --- tools
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required ($2)"; }
need cmake "install it with your package manager, version 3.18 or newer"
need tar "to unpack the source"
command -v c++ >/dev/null 2>&1 || command -v g++ >/dev/null 2>&1 || command -v clang++ >/dev/null 2>&1 || command -v cl >/dev/null 2>&1 ||
  die "a C++ compiler is required (g++, clang++, or MSVC cl)"
if command -v curl >/dev/null 2>&1; then fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then fetch() { wget -q -O "$2" "$1"; }
else die "curl or wget is required to download the source"; fi
if command -v sha256sum >/dev/null 2>&1; then sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else die "sha256sum or shasum is required to check the download"; fi
if [ -z "$jobs" ]; then
  jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)"
fi

# --- CUDA: nvcc from CUDACXX, CUDA_HOME, $PATH or the usual install folders
nvcc=""
for c in "${CUDACXX:-}" "${CUDA_HOME:+$CUDA_HOME/bin/nvcc}" "$(command -v nvcc 2>/dev/null || true)" \
  /usr/local/cuda/bin/nvcc /opt/cuda/bin/nvcc; do
  if [ -n "$c" ] && [ -x "$c" ]; then nvcc="$c"; break; fi
done
case "$mode" in
  cuda) [ -n "$nvcc" ] || die "--cuda: nvcc not found (install the CUDA toolkit, or set CUDACXX=/path/to/nvcc)" ;;
  auto) if [ -n "$nvcc" ]; then mode=cuda; else mode=cpu; fi ;;
esac
cmake_args=(-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_TESTS=OFF
	-DWHISPER_BUILD_SERVER=OFF -DWHISPER_SDL2=OFF -DWHISPER_CURL=OFF -DGGML_NATIVE=OFF
	-DGGML_SSE42=OFF -DGGML_AVX=OFF -DGGML_AVX2=OFF -DGGML_AVX_VNNI=OFF -DGGML_BMI2=OFF
	-DGGML_AVX512=OFF -DGGML_FMA=OFF -DGGML_F16C=OFF -DGGML_METAL=OFF -DGGML_OPENMP=OFF
	-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded)
if [ "$(uname -s)" = Darwin ]; then
  cmake_args+=("-DCMAKE_OSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET:-12.0}" -DGGML_BLAS=OFF)
fi
if [ "$mode" = cuda ]; then
  say "CUDA build with $nvcc"
  cmake_args+=(-DGGML_CUDA=ON "-DCMAKE_CUDA_COMPILER=$nvcc")
elif [ -n "$nvcc" ]; then
  say "CPU build (--cpu given)"
else
  say "CPU build (no nvcc found; install the CUDA toolkit or set CUDACXX for a GPU build)"
fi

# --- source: the pinned release tarball, checked against its sha256
work="${XDG_CACHE_HOME:-$HOME/.cache}/retake/build"
mkdir -p "$work"
tarball="$work/whisper.cpp-$TAG.tar.gz"
if [ ! -f "$tarball" ] || [ "$(sha "$tarball")" != "$SHA256" ]; then
  say "downloading whisper.cpp $TAG"
  fetch "$URL" "$tarball.part"
  mv "$tarball.part" "$tarball"
fi
got="$(sha "$tarball")"
[ "$got" = "$SHA256" ] || { rm -f "$tarball"; die "checksum mismatch for $tarball: got $got, want $SHA256"; }
src="$work/whisper.cpp-$TAG-$mode"
rm -rf "$src"
mkdir -p "$src"
tar -xzf "$tarball" -C "$src" --strip-components=1

# --- build
say "building whisper-cli with $jobs jobs (a CUDA build takes a while)"
cmake -S "$src" -B "$src/build" "${cmake_args[@]}" >"$src/configure.log" 2>&1 ||
  { tail -n 30 "$src/configure.log" >&2; die "cmake configure failed (full log: $src/configure.log)"; }
cmake --build "$src/build" --config Release --target whisper-cli -j "$jobs" >"$src/build.log" 2>&1 ||
  { tail -n 30 "$src/build.log" >&2; die "build failed (full log: $src/build.log)"; }
bin=""
for candidate in "$src/build/bin/whisper-cli" "$src/build/bin/whisper-cli.exe" "$src/build/bin/Release/whisper-cli.exe"; do
  [ -x "$candidate" ] && { bin="$candidate"; break; }
done
[ -n "$bin" ] || die "the build finished but whisper-cli is missing"
"$bin" --help >/dev/null 2>&1 || die "the new whisper-cli doesn't run ($bin --help failed)"

# --- install: replace the binary atomically, then record what was built
mkdir -p "$prefix/bin"
suffix=""
case "$bin" in *.exe) suffix=".exe" ;; esac
cp "$bin" "$prefix/bin/.whisper-cli.new$suffix"
mv -f "$prefix/bin/.whisper-cli.new$suffix" "$prefix/bin/whisper-cli$suffix"
printf 'whisper.cpp %s\nbackend %s\n' "$TAG" "$mode" >"$prefix/BUILD"
if [ "$keep" = 0 ]; then rm -rf "$src"; fi
say "installed $prefix/bin/whisper-cli ($mode)"
