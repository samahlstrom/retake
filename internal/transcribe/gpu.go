package transcribe

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GPU is one CUDA device and its memory, in MiB.
type GPU struct {
	Name              string
	FreeMiB, TotalMiB int64
}

// nvidiaGPUs asks nvidia-smi for the CUDA devices. No nvidia-smi means no usable NVIDIA GPU.
func nvidiaGPUs(ctx context.Context) ([]GPU, error) {
	smi, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, smi, "--query-gpu=name,memory.free,memory.total", //nolint:gosec // fixed args
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	return parseNvidiaSMI(out), nil
}

// parseNvidiaSMI parses lines like "NVIDIA GeForce RTX 4090, 22897, 24564".
func parseNvidiaSMI(out []byte) []GPU {
	var gpus []GPU
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ",")
		if len(f) < 3 {
			continue
		}
		n := len(f)
		free, err1 := strconv.ParseInt(strings.TrimSpace(f[n-2]), 10, 64)
		total, err2 := strconv.ParseInt(strings.TrimSpace(f[n-1]), 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		gpus = append(gpus, GPU{Name: strings.TrimSpace(strings.Join(f[:n-2], ",")), FreeMiB: free, TotalMiB: total})
	}
	return gpus
}

// gpuNeedMiB is the GPU memory a model needs: its weights plus room for the KV cache and compute buffers.
func gpuNeedMiB(model string) int64 {
	var size int64
	if f, ok := modelFiles[model]; ok {
		size = f.Size
	} else if st, err := os.Stat(model); err == nil {
		size = st.Size()
	}
	return size*6/5>>20 + 1024
}

// binaryHasCUDA reports whether a whisper-cli build includes the CUDA backend.
//   - install-whisper.sh writes <prefix>/BUILD beside <prefix>/bin, naming the backend.
//   - A shared-library build has libggml-cuda beside the binary or in ../lib.
//   - A static build carries the CUDA backend's code, and with it the string "ggml_cuda_init".
func binaryHasCUDA(cli string) bool {
	dir := filepath.Dir(cli)
	if b, err := os.ReadFile(filepath.Join(dir, "..", "BUILD")); err == nil { //nolint:gosec // beside the binary
		switch {
		case bytes.Contains(b, []byte("backend cuda")):
			return true
		case bytes.Contains(b, []byte("backend cpu")):
			return false
		}
	}
	for _, d := range []string{dir, filepath.Join(dir, "..", "lib"), filepath.Join(dir, "..", "lib64")} {
		if m, _ := filepath.Glob(filepath.Join(d, "*ggml-cuda*")); len(m) > 0 {
			return true
		}
	}
	return fileContains(cli, []byte("ggml_cuda_init"))
}

// fileContains reports whether the file at path contains needle, reading it in chunks.
func fileContains(path string, needle []byte) bool {
	f, err := os.Open(path) //nolint:gosec // the configured binary
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 1<<20+len(needle))
	keep := 0 // bytes carried over from the last chunk, so a match across chunks is found
	for {
		n, err := io.ReadFull(f, buf[keep:])
		end := keep + n
		if bytes.Contains(buf[:end], needle) {
			return true
		}
		if err != nil {
			return false
		}
		keep = min(len(needle)-1, end)
		copy(buf, buf[end-keep:end])
	}
}
