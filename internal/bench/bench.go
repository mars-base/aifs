// Package bench provides filesystem I/O benchmarking used by both the CLI
// (aifs bench) and the GUI.
package bench

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config holds benchmark parameters.
type Config struct {
	// BlockSize is the read I/O block size in bytes (default 1 MiB).
	BlockSize int64
	// BigSize is the size of each big file in bytes (0 = skip big-file tests).
	BigSize int64
	// SmallSize is the size of each small file in bytes (default 128 KiB).
	SmallSize int64
	// SmallCount is the number of small files per thread (default 10).
	SmallCount int
	// Threads is the number of concurrent goroutines (default 1).
	Threads int
}

// DefaultConfig returns Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		BlockSize:  1 << 20,       // 1 MiB
		BigSize:    100 << 20,     // 100 MiB
		SmallSize:  128 << 10,     // 128 KiB
		SmallCount: 10,
		Threads:    1,
	}
}

// Result holds the benchmark measurements.
type Result struct {
	// Big-file write throughput in MiB/s (0 if skipped).
	WriteBigMiBs float64
	// Big-file write latency in seconds per file (0 if skipped).
	WriteBigSecsPerFile float64
	// Big-file read throughput in MiB/s (0 if skipped).
	ReadBigMiBs float64
	// Big-file read latency in seconds per file (0 if skipped).
	ReadBigSecsPerFile float64
	// Small-file write rate in files/s.
	WriteSmallPerSec float64
	// Small-file write latency in ms per file.
	WriteSmallMsPerFile float64
	// Small-file read rate in files/s.
	ReadSmallPerSec float64
	// Small-file read latency in ms per file.
	ReadSmallMsPerFile float64
	// Stat rate in files/s.
	StatPerSec float64
	// Stat latency in ms per file.
	StatMsPerFile float64

	// Parameters echoed back for display.
	BlockSize  int64
	BigSize    int64
	SmallSize  int64
	SmallCount int
	Threads    int
}

// Run executes all benchmark stages under path and returns the results.
// It creates a temporary directory inside path, performs timed I/O in parallel
// goroutines, and removes the directory on return.
func Run(path string, cfg Config) (Result, error) {
	if cfg.BlockSize <= 0 {
		return Result{}, fmt.Errorf("bench: BlockSize must be > 0")
	}
	if cfg.SmallSize <= 0 {
		return Result{}, fmt.Errorf("bench: SmallSize must be > 0")
	}
	if cfg.Threads <= 0 {
		cfg.Threads = 1
	}
	if cfg.SmallCount <= 0 {
		cfg.SmallCount = 0
	}

	root, err := filepath.Abs(path)
	if err != nil {
		return Result{}, fmt.Errorf("bench: resolving path: %w", err)
	}
	tmpdir := filepath.Join(root, fmt.Sprintf("__aifs_bench_%d__", os.Getpid()))
	if err := os.MkdirAll(tmpdir, 0o777); err != nil {
		return Result{}, fmt.Errorf("bench: creating bench dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()

	res := Result{
		BlockSize:  cfg.BlockSize,
		BigSize:    cfg.BigSize,
		SmallSize:  cfg.SmallSize,
		SmallCount: cfg.SmallCount,
		Threads:    cfg.Threads,
	}

	// runParallel runs fn(threadIndex) in cfg.Threads goroutines and returns
	// the wall-clock duration (at least 1µs to avoid division by zero).
	runParallel := func(fn func(int)) time.Duration {
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < cfg.Threads; i++ {
			i := i
			wg.Add(1)
			go func() { defer wg.Done(); fn(i) }()
		}
		wg.Wait()
		d := time.Since(start)
		if d < time.Microsecond {
			d = time.Microsecond
		}
		return d
	}

	// ── big file ─────────────────────────────────────────────────────────────
	if cfg.BigSize > 0 {
		// Round up to a multiple of BlockSize.
		blocks := (cfg.BigSize + cfg.BlockSize - 1) / cfg.BlockSize
		actualBigSize := blocks * cfg.BlockSize

		// Pre-generate full content for one-shot write.  Writing the file in a
		// single fp.Write() call avoids PostgreSQL TOAST Read-Modify-Write
		// amplification: each partial write to an existing 4 MiB TOAST chunk
		// requires a read + modify + re-insert of the whole chunk.  One-shot
		// write produces only INSERT operations (no UPDATE), dramatically faster.
		bigBuf := make([]byte, actualBigSize)
		rand.Read(bigBuf) //nolint:gosec

		dur := runParallel(func(idx int) {
			fname := filepath.Join(tmpdir, fmt.Sprintf("big.%d", idx))
			fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
				return
			}
			if _, err := fp.Write(bigBuf); err != nil {
				fmt.Fprintf(os.Stderr, "bench: write %s: %v\n", fname, err)
			}
			_ = fp.Close()
		})
		totalMiB := float64(actualBigSize) * float64(cfg.Threads) / (1024 * 1024)
		secs := dur.Seconds()
		res.WriteBigMiBs = totalMiB / secs
		res.WriteBigSecsPerFile = secs / float64(cfg.Threads)

		readBuf := make([]byte, cfg.BlockSize)
		dur = runParallel(func(idx int) {
			fname := filepath.Join(tmpdir, fmt.Sprintf("big.%d", idx))
			fp, err := os.Open(fname)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
				return
			}
			for b := int64(0); b < blocks; b++ {
				if _, err := fp.Read(readBuf); err != nil {
					break
				}
			}
			_ = fp.Close()
		})
		secs = dur.Seconds()
		res.ReadBigMiBs = totalMiB / secs
		res.ReadBigSecsPerFile = secs / float64(cfg.Threads)
	}

	// ── small files ───────────────────────────────────────────────────────────
	if cfg.SmallCount > 0 {
		// Pre-generate full small-file content for one-shot write.
		smallBuf := make([]byte, cfg.SmallSize)
		rand.Read(smallBuf) //nolint:gosec

		dur := runParallel(func(idx int) {
			for i := 0; i < cfg.SmallCount; i++ {
				fname := filepath.Join(tmpdir, fmt.Sprintf("small.%d.%d", idx, i))
				fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
				if err != nil {
					fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
					continue
				}
				if _, err := fp.Write(smallBuf); err != nil {
					fmt.Fprintf(os.Stderr, "bench: write %s: %v\n", fname, err)
				}
				_ = fp.Close()
			}
		})
		totalFiles := float64(cfg.SmallCount * cfg.Threads)
		secs := dur.Seconds()
		res.WriteSmallPerSec = totalFiles / secs
		res.WriteSmallMsPerFile = secs * 1000 / totalFiles

		rsbuf := make([]byte, cfg.SmallSize)
		dur = runParallel(func(idx int) {
			for i := 0; i < cfg.SmallCount; i++ {
				fname := filepath.Join(tmpdir, fmt.Sprintf("small.%d.%d", idx, i))
				fp, err := os.Open(fname)
				if err != nil {
					fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
					continue
				}
				if _, err := fp.Read(rsbuf); err != nil {
					fmt.Fprintf(os.Stderr, "bench: read %s: %v\n", fname, err)
				}
				_ = fp.Close()
			}
		})
		secs = dur.Seconds()
		res.ReadSmallPerSec = totalFiles / secs
		res.ReadSmallMsPerFile = secs * 1000 / totalFiles

		dur = runParallel(func(idx int) {
			for i := 0; i < cfg.SmallCount; i++ {
				fname := filepath.Join(tmpdir, fmt.Sprintf("small.%d.%d", idx, i))
				_, _ = os.Stat(fname)
			}
		})
		secs = dur.Seconds()
		res.StatPerSec = totalFiles / secs
		res.StatMsPerFile = secs * 1000 / totalFiles
	}

	return res, nil
}
