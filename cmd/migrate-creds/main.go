// Command migrate-creds performs a one-time migration of
// AXIOM_AGENT_API_KEY from the temporary env-var bridge (see
// internal/uploader's package doc) into the real per-user encrypted
// credstore (Tier 2 #8).
//
// Reads the value from the environment only -- never accepts it as a
// CLI argument, which would leak it into shell history and process
// listings (ps aux). Never prints the value itself, only whether
// migration succeeded, verified by an explicit readback rather than
// assumed from a successful write (Golden Rule 9).
//
// Usage:
//
//	set -a; source ~/.axiom-agent/credentials.env; set +a
//	./migrate-creds
package main

import (
	"fmt"
	"os"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/credstore"
)

func main() {
	val := os.Getenv("AXIOM_AGENT_API_KEY")
	if val == "" {
		fmt.Fprintln(os.Stderr, "migrate-creds: AXIOM_AGENT_API_KEY is not set in the environment -- "+
			"did you source credentials.env first? Aborting, nothing written.")
		os.Exit(1)
	}

	store, err := credstore.NewDefaultUserFileStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-creds: could not open credential store: %v\n", err)
		os.Exit(1)
	}

	if err := store.Set("AXIOM_AGENT_API_KEY", val); err != nil {
		fmt.Fprintf(os.Stderr, "migrate-creds: failed to write credential: %v\n", err)
		os.Exit(1)
	}

	// Verify by reading it back -- never assume a write succeeded.
	// Never print the value itself, only whether it matches.
	readback, err := store.Get("AXIOM_AGENT_API_KEY")
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-creds: wrote credential but readback failed: %v\n", err)
		os.Exit(1)
	}
	if readback != val {
		fmt.Fprintln(os.Stderr, "migrate-creds: readback value does not match what was written -- aborting, do not trust this migration")
		os.Exit(1)
	}

	fmt.Printf("migrate-creds: OK -- AXIOM_AGENT_API_KEY migrated to credstore (%d bytes), verified by readback.\n", len(val))
}
