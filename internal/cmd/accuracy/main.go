// Command accuracy fetches the public benchmark sets and measures dialect
// detection on them.
//
//	go run ./internal/cmd/accuracy fetch --dir DIR
//	go run ./internal/cmd/accuracy run --dir DIR --split dev [--out FILE]
//	go run ./internal/cmd/accuracy run --dir DIR --split heldout --final --out FILE
//
// The held-out half and the whole set are run only with --final, and every
// such run is appended to heldout-runs.log beside the output file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Altitud-Studio-S-L-U/wildcsv/internal/accuracy"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetch(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "accuracy:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: accuracy fetch --dir DIR | accuracy run --dir DIR --split dev|heldout|all [--final] [--out FILE]")
	os.Exit(2)
}

func git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

func fetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	dir := fs.String("dir", "", "directory for the checkout")
	fs.Parse(args)
	if *dir == "" {
		return errors.New("--dir is required")
	}
	if out, err := exec.Command("git", "-C", *dir, "rev-parse", "HEAD").Output(); err == nil {
		if strings.TrimSpace(string(out)) == accuracy.Commit {
			fmt.Fprintln(os.Stderr, "already at", accuracy.Commit)
			return nil
		}
		return fmt.Errorf("%s holds another checkout", *dir)
	}
	if err := git("clone", "--filter=blob:none", "--no-checkout", accuracy.Repository, *dir); err != nil {
		return err
	}
	if err := git(append([]string{"-C", *dir, "sparse-checkout", "set", "--cone"}, accuracy.SparsePaths...)...); err != nil {
		return err
	}
	return git("-C", *dir, "-c", "advice.detachedHead=false", "checkout", accuracy.Commit)
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dir := fs.String("dir", "", "checkout made by fetch")
	split := fs.String("split", accuracy.Dev, "dev, heldout or all")
	final := fs.Bool("final", false, "allow the held-out half and the whole set")
	out := fs.String("out", "", "write a per-file table here")
	fs.Parse(args)
	if *dir == "" {
		return errors.New("--dir is required")
	}
	switch *split {
	case accuracy.Dev:
	case accuracy.HeldOut, accuracy.All:
		if !*final {
			return errors.New("the held-out half and the whole set are run only with --final")
		}
		if *out == "" {
			return errors.New("--final needs --out, beside which the run is logged")
		}
	default:
		return fmt.Errorf("unknown split %q", *split)
	}
	rep, err := accuracy.Run(*dir, *split)
	if err != nil {
		return err
	}
	printReport(os.Stdout, rep)
	if *out != "" {
		if err := writeFiles(*out, rep); err != nil {
			return err
		}
	}
	if *final {
		head, _ := exec.Command("git", "rev-parse", "HEAD").Output()
		log := filepath.Join(filepath.Dir(*out), "heldout-runs.log")
		f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(f, "%s\t%s\t%s\tweighted F1 %.4f\n", time.Now().UTC().Format(time.RFC3339), *split,
			strings.TrimSpace(string(head)), accuracy.WeightedF1(rep.Rows))
	}
	return nil
}

func printReport(w io.Writer, rep *accuracy.Report) {
	fmt.Fprintf(w, "split %s · %s/%s · Go %s\n\n", rep.Split, runtime.GOOS, runtime.GOARCH, runtime.Version())
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "row\tfiles\tpasses\tfailures\taccuracy\tN\tsuccess ratio\tprecision\trecall\tF1\t")
	runSum, passSum := 0, 0
	for _, r := range rep.Rows {
		p, rec, f1 := r.PRF()
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%.2f%%\t%d\t%.2f%%\t%.4f\t%.4f\t%.4f\t\n",
			r.Row, r.Run, r.Passes, r.Failures, 100*r.Accuracy(), r.N, 100*r.SuccessRatio(), p, rec, f1)
		runSum += r.Run
		passSum += r.Passes
	}
	tw.Flush()
	fmt.Fprintf(w, "\nweighted F1 %.4f · passes %d of %d file runs\n", accuracy.WeightedF1(rep.Rows), passSum, runSum)
	fmt.Fprintf(w, "detection time: median %v, p95 %v, max %v\n", rep.Percentile(50), rep.Percentile(95), rep.Percentile(100))
	fmt.Fprintln(w, "\nconfidence band\tfiles\taccuracy")
	for _, b := range rep.Bands() {
		acc := 0.0
		if b.Files > 0 {
			acc = 100 * float64(b.Passes) / float64(b.Files)
		}
		fmt.Fprintf(w, "[%.1f, %.1f)\t%d\t%.1f%%\n", b.Low, min(b.High, 1), b.Files, acc)
	}
}

func writeFiles(path string, rep *accuracy.Report) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintln(f, "row\tfile\texpected\tdetected\tpass\tfailed\tconfidence\tmicroseconds")
	for _, r := range rep.Files {
		fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%v\t%v\t%.3f\t%d\n", r.Row, r.File, r.Expected, r.Detected, r.Pass, r.Failed, r.Confidence, r.Elapsed.Microseconds())
	}
	return f.Close()
}
