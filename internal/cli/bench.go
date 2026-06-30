package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/bench"
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

	cfg := bench.Config{
		BlockSize:  blockSize,
		BigSize:    bigSize,
		SmallSize:  smallSize,
		SmallCount: int(benchSmallCount),
		Threads:    threads,
	}

	if cfg.BigSize > 0 {
		fmt.Printf("Writing 1 big file(s) × %d thread(s) ...\n", threads)
	}
	// Progress messages are printed inside bench.Run via fmt.Fprintf(os.Stderr)
	// for non-progress output; here we print phase headers before calling Run.
	// Re-print headers inline to match the previous UX.
	res, err := bench.Run(args[0], cfg)
	if err != nil {
		return err
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

	type row struct{ item, value, cost string }
	var rows []row

	if bigSize > 0 {
		rows = append(rows,
			row{"Write big file",
				fmt.Sprintf("%.2f MiB/s", res.WriteBigMiBs),
				fmt.Sprintf("%.2f s/file", res.WriteBigSecsPerFile)},
			row{"Read big file",
				fmt.Sprintf("%.2f MiB/s", res.ReadBigMiBs),
				fmt.Sprintf("%.2f s/file", res.ReadBigSecsPerFile)},
		)
	}
	if cfg.SmallCount > 0 {
		rows = append(rows,
			row{"Write small file",
				fmt.Sprintf("%.1f files/s", res.WriteSmallPerSec),
				fmt.Sprintf("%.2f ms/file", res.WriteSmallMsPerFile)},
			row{"Read small file",
				fmt.Sprintf("%.1f files/s", res.ReadSmallPerSec),
				fmt.Sprintf("%.2f ms/file", res.ReadSmallMsPerFile)},
			row{"Stat file",
				fmt.Sprintf("%.1f files/s", res.StatPerSec),
				fmt.Sprintf("%.2f ms/file", res.StatMsPerFile)},
		)
	}

	header := []string{"ITEM", "VALUE", "COST"}
	var tableRows [][]string
	tableRows = append(tableRows, header)
	for _, r := range rows {
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
