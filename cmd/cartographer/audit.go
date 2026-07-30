package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"crypto/ed25519"

	"github.com/BeppeTemp/cartographer/internal/audit"
)

// cmdAudit is the operator entry point to the compliance log (D119). It is
// deliberately offline: it reads the files on disk and never talks to a
// running server, so it still works when the server is down — which is when
// an audit trail is usually needed.
func cmdAudit(args []string) int {
	if len(args) == 0 {
		printAuditUsage(os.Stderr)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "verify":
		return cmdAuditVerify(rest)
	case "export":
		return cmdAuditExport(rest)
	case "help", "-h", "--help":
		printAuditUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown audit subcommand %q\n\n", sub)
		printAuditUsage(os.Stderr)
		return 2
	}
}

func printAuditUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: cartographer audit <verify|export> --log <path> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  verify   Validate the hash chain across the active log, the archived")
	fmt.Fprintln(w, "           segments and the signed checkpoint index.")
	fmt.Fprintln(w, "  export   Write the verified entries as JSON for an external reviewer.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --log <path>         Active audit log (required)")
	fmt.Fprintln(w, "  --archive <dir>      Archive directory, if not the default beside --log")
	fmt.Fprintln(w, "  --public-key <hex>   Ed25519 public key; required once the log is signed")
	fmt.Fprintln(w, "  --out <path>         export only: output file (default stdout)")
}

// auditFlags are shared by both subcommands so an operator does not have to
// remember which one takes which flag.
type auditFlags struct {
	log       string
	archive   string
	publicKey string
	out       string
}

func bindAuditFlags(fs *flag.FlagSet, f *auditFlags, withOut bool) {
	fs.StringVar(&f.log, "log", "", "Path to the active audit log")
	fs.StringVar(&f.archive, "archive", "", "Archive directory for rotated segments")
	fs.StringVar(&f.publicKey, "public-key", "", "Ed25519 public key in hex")
	if withOut {
		fs.StringVar(&f.out, "out", "", "Output file (default stdout)")
	}
}

// resolvePublicKey returns nil when no key is configured: VerifyAudit then
// checks the chain but not the signatures, and reports unsigned entries so the
// caller can tell the two situations apart.
func resolvePublicKey(hexKey string) (ed25519.PublicKey, error) {
	if hexKey == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid --public-key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid --public-key: got %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func cmdAuditVerify(args []string) int {
	fs := flag.NewFlagSet("audit verify", flag.ExitOnError)
	var f auditFlags
	bindAuditFlags(fs, &f, false)
	fs.Parse(args)

	summary, code := runAuditVerify(f)
	if code != 0 {
		return code
	}

	fmt.Printf("valid:           %d\n", summary.Valid)
	fmt.Printf("unsigned:        %d\n", summary.Unsigned)
	fmt.Printf("incomplete:      %d\n", summary.Incomplete)
	fmt.Printf("segments:        %d retained, %d checkpoint-only\n", summary.Retained, summary.CheckpointOnly)
	fmt.Printf("corrupt:         %d\n", summary.Corrupt)
	if summary.Corrupt > 0 {
		fmt.Fprintln(os.Stderr, "audit: chain verification FAILED")
		return 1
	}
	fmt.Println("audit: chain verified")
	return 0
}

func cmdAuditExport(args []string) int {
	fs := flag.NewFlagSet("audit export", flag.ExitOnError)
	var f auditFlags
	bindAuditFlags(fs, &f, true)
	fs.Parse(args)

	summary, code := runAuditVerify(f)
	if code != 0 {
		return code
	}
	// Exporting an unverified chain would produce a document that looks
	// authoritative without being one, so a corrupt chain refuses to export.
	if summary.Corrupt > 0 {
		fmt.Fprintf(os.Stderr, "Error: refusing to export: %d corrupt entr(ies)\n", summary.Corrupt)
		return 1
	}

	out, err := json.MarshalIndent(map[string]any{
		"valid":           summary.Valid,
		"unsigned":        summary.Unsigned,
		"incomplete":      summary.Incomplete,
		"retained":        summary.Retained,
		"checkpoint_only": summary.CheckpointOnly,
		"events":          summary.Events,
	}, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	out = append(out, '\n')

	if f.out == "" {
		os.Stdout.Write(out)
		return 0
	}
	if err := os.WriteFile(f.out, out, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "audit: exported %d event(s) to %s\n", len(summary.Events), f.out)
	return 0
}

// runAuditVerify centralizes flag validation and the verification call shared
// by both subcommands.
func runAuditVerify(f auditFlags) (audit.Summary, int) {
	if f.log == "" {
		fmt.Fprintln(os.Stderr, "Error: --log is required")
		printAuditUsage(os.Stderr)
		return audit.Summary{}, 2
	}
	pub, err := resolvePublicKey(f.publicKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return audit.Summary{}, 2
	}
	summary, err := audit.VerifyAudit(f.log, f.archive, pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return audit.Summary{}, 1
	}
	return summary, 0
}
