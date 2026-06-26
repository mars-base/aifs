package cli

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(benchCmd)
	benchCmd.Flags().StringVar(&benchBlockSize, "block-size", "1M", "size of each I/O block (e.g. 1M, 512K)")
	benchCmd.Flags().StringVar(&benchBigSize, "big-file-size", "100M", "size of each big file (0 to skip, e.g. 1G, 512M)")
	benchCmd.Flags().StringVar(&benchSmallSize, "small-file-size", "128K", "size of each small file (e.g. 128K)")
	benchCmd.Flags().UintVar(&benchSmallCount, "small-file-count", 10, "number of small files per thread")
	benchCmd.Flags().UintVarP(&benchThreads, "threads", "p", 1, "number of concurrent threads")
}

var (
	benchBlockSize  string
	benchBigSize    string
	benchSmallSize  string
	benchSmallCount uint
	benchThreads    uint
)

var benchCmd = &cobra.Command{
	Use:   "bench <path>",
	Short: "Run basic I/O benchmarks on a path",
	Long: `bench measures write/read throughput and metadata performance on the target path.

It creates a temporary directory inside <path>, runs timed I/O operations in parallel
threads, then removes the temporary directory.

Examples:
  aifs bench ~/mnt/ai01
  aifs bench ~/mnt/ai01 --big-file-size 0          # small files only
  aifs bench ~/mnt/ai01 -p 4                        # 4 concurrent threads
  aifs bench /tmp                                    # baseline against local disk`,
	Args: cobra.ExactArgs(1),
	RunE: runBench,
}

func runBench(cmd *cobra.Command, args []string) error {
	blockSize, err := parseSize(benchBlockSize)
	if err != nil || blockSize == 0 {
		return fmt.Errorf("invalid --block-size %q", benchBlockSize)
	}
	bigSize, err := parseSize(benchBigSize)
	if err != nil {
		return fmt.Errorf("invalid --big-file-size %q", benchBigSize)
	}
	smallSize, err := parseSize(benchSmallSize)
	if err != nil || smallSize == 0 {
		return fmt.Errorf("invalid --small-file-size %q", benchSmallSize)
	}
	threads := int(benchThreads)
	if threads == 0 {
		return fmt.Errorf("--threads must be >= 1")
	}
	smallCount := int(benchSmallCount)

	root, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}
	tmpdir := filepath.Join(root, fmt.Sprintf("__aifs_bench_%d__", os.Getpid()))
	if err := os.MkdirAll(tmpdir, 0o777); err != nil {
		return fmt.Errorf("creating bench dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpdir)
	}()

	// Pre-generate random buffer (reused across writes to avoid CPU bottleneck).
	buf := make([]byte, blockSize)
	rand.Read(buf) //nolint:gosec

	dropCaches := func() {
		if os.Getenv("SKIP_DROP_CACHES") == "1" {
			return
		}
		f, err := os.OpenFile("/proc/sys/vm/drop_caches", os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = f.WriteString("3\n")
		_ = f.Close()
	}

	type row struct {
		item, value, cost string
	}
	var results []row

	// ── helpers ──────────────────────────────────────────────────────────────

	// runParallel runs fn(threadIndex) in `threads` goroutines and returns
	// the wall-clock duration. Always returns at least 1µs to avoid division
	// by zero when operations complete faster than timer resolution.
	runParallel := func(fn func(int)) time.Duration {
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < threads; i++ {
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
	if bigSize > 0 {
		// Adjust bigSize to be a multiple of blockSize.
		blocks := (bigSize + blockSize - 1) / blockSize
		actualBigSize := blocks * blockSize

		fmt.Printf("Writing %d big file(s) × %d thread(s) ...\n",
			1, threads)
		dur := runParallel(func(idx int) {
			fname := filepath.Join(tmpdir, fmt.Sprintf("big.%d", idx))
			fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
				return
			}
			for b := int64(0); b < blocks; b++ {
				if _, err := fp.Write(buf); err != nil {
					fmt.Fprintf(os.Stderr, "bench: write %s: %v\n", fname, err)
					break
				}
			}
			_ = fp.Close()
		})
		totalMiB := float64(actualBigSize) * float64(threads) / (1024 * 1024)
		secs := dur.Seconds()
		results = append(results, row{
			item:  "Write big file",
			value: fmt.Sprintf("%.2f MiB/s", totalMiB/secs),
			cost:  fmt.Sprintf("%.2f s/file", secs/float64(threads)),
		})
		dropCaches()

		fmt.Printf("Reading %d big file(s) × %d thread(s) ...\n", 1, threads)
		readBuf := make([]byte, blockSize)
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
		totalMiB = float64(actualBigSize) * float64(threads) / (1024 * 1024)
		secs = dur.Seconds()
		results = append(results, row{
			item:  "Read big file",
			value: fmt.Sprintf("%.2f MiB/s", totalMiB/secs),
			cost:  fmt.Sprintf("%.2f s/file", secs/float64(threads)),
		})
		dropCaches()
	}

	// ── small files ───────────────────────────────────────────────────────────
	if smallCount > 0 {
		// small files: bsize = min(smallSize, blockSize)
		sbuf := buf
		if smallSize < blockSize {
			sbuf = buf[:smallSize]
		}
		sbsize := int64(len(sbuf))
		sblocks := (smallSize + sbsize - 1) / sbsize

		fmt.Printf("Writing %d small file(s) × %d thread(s) ...\n",
			smallCount, threads)
		dur := runParallel(func(idx int) {
			for i := 0; i < smallCount; i++ {
				fname := filepath.Join(tmpdir, fmt.Sprintf("small.%d.%d", idx, i))
				fp, err := os.OpenFile(fname, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
				if err != nil {
					fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
					continue
				}
				for b := int64(0); b < sblocks; b++ {
					if _, err := fp.Write(sbuf); err != nil {
						break
					}
				}
				_ = fp.Close()
			}
		})
		totalFiles := float64(smallCount * threads)
		secs := dur.Seconds()
		results = append(results, row{
			item:  "Write small file",
			value: fmt.Sprintf("%.1f files/s", totalFiles/secs),
			cost:  fmt.Sprintf("%.2f ms/file", secs*1000/totalFiles),
		})
		dropCaches()

		fmt.Printf("Reading %d small file(s) × %d thread(s) ...\n",
			smallCount, threads)
		rsbuf := make([]byte, sbsize)
		dur = runParallel(func(idx int) {
			for i := 0; i < smallCount; i++ {
				fname := filepath.Join(tmpdir, fmt.Sprintf("small.%d.%d", idx, i))
				fp, err := os.Open(fname)
				if err != nil {
					fmt.Fprintf(os.Stderr, "bench: open %s: %v\n", fname, err)
					continue
				}
				for b := int64(0); b < sblocks; b++ {
					if _, err := fp.Read(rsbuf); err != nil {
						break
					}
				}
				_ = fp.Close()
			}
		})
		secs = dur.Seconds()
		results = append(results, row{
			item:  "Read small file",
			value: fmt.Sprintf("%.1f files/s", totalFiles/secs),
			cost:  fmt.Sprintf("%.2f ms/file", secs*1000/totalFiles),
		})
		dropCaches()

		fmt.Printf("Stat %d small file(s) × %d thread(s) ...\n",
			smallCount, threads)
		dur = runParallel(func(idx int) {
			for i := 0; i < smallCount; i++ {
				fname := filepath.Join(tmpdir, fmt.Sprintf("small.%d.%d", idx, i))
				_, _ = os.Stat(fname)
			}
		})
		secs = dur.Seconds()
		results = append(results, row{
			item:  "Stat file",
			value: fmt.Sprintf("%.1f files/s", totalFiles/secs),
			cost:  fmt.Sprintf("%.2f ms/file", secs*1000/totalFiles),
		})
	}

	// ── print results ─────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("BlockSize: %s, BigFileSize: %s, SmallFileSize: %s, SmallFileCount: %d, NumThreads: %d\n",
		humanizeBytes(blockSize),
		humanizeBytes(bigSize),
		humanizeBytes(smallSize),
		benchSmallCount,
		benchThreads,
	)

	// Build header + data rows as string slices for the table printer.
	header := []string{"ITEM", "VALUE", "COST"}
	var tableRows [][]string
	tableRows = append(tableRows, header)
	for _, r := range results {
		tableRows = append(tableRows, []string{r.item, r.value, r.cost})
	}
	printBenchTable(tableRows)
	return nil
}

// printBenchTable prints rows as a simple ASCII box table.
// rows[0] is the header row.
func printBenchTable(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	cols := len(rows[0])
	widths := make([]int, cols)
	for _, row := range rows {
		for c, cell := range row {
			if len(cell) > widths[c] {
				widths[c] = len(cell)
			}
		}
	}

	divider := func() string {
		var b strings.Builder
		for _, w := range widths {
			b.WriteByte('+')
			b.WriteString(strings.Repeat("-", w+2))
		}
		b.WriteByte('+')
		return b.String()
	}

	printRow := func(cells []string, center bool) {
		var b strings.Builder
		for c, cell := range cells {
			b.WriteString("| ")
			if center {
				pad := widths[c] - len(cell)
				left := pad / 2
				right := pad - left
				b.WriteString(strings.Repeat(" ", left))
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", right))
			} else {
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", widths[c]-len(cell)))
			}
			b.WriteString(" ")
		}
		b.WriteByte('|')
		fmt.Println(b.String())
	}

	div := divider()
	fmt.Println(div)
	printRow(rows[0], true) // header centred
	fmt.Println(div)
	for _, row := range rows[1:] {
		printRow(row, false)
	}
	fmt.Println(div)
}

// parseSize parses strings like "1G", "512M", "128K", "1048576" into bytes.
// Returns 0, nil for the string "0".
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	mul := int64(1)
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "G"):
		mul = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(upper, "M"):
		mul = 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(upper, "K"):
		mul = 1024
		s = s[:len(s)-1]
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("cannot parse %q as a size", s)
	}
	return n * mul, nil
}

// humanizeBytes formats a byte count as "N GiB", "N MiB", "N KiB", or "N B".
func humanizeBytes(n int64) string {
	switch {
	case n == 0:
		return "0"
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%d GiB", n/(1024*1024*1024))
	case n >= 1024*1024:
		return fmt.Sprintf("%d MiB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%d KiB", n/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
