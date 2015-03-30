package transactionpool

import (
	"errors"
	"strings"

	"github.com/NebulousLabs/Sia/consensus"
	"github.com/NebulousLabs/Sia/encoding"
	"github.com/NebulousLabs/Sia/modules"
)

// standard.go adds extra rules to transactions which help preserve network
// health and provides flexibility for future soft forks and tweaks to the
// network.

const (
	FileContractConfirmWindow = 8
	TransactionSizeLimit      = 16 * 1024

	PrefixNonSia = "NonSia"
)

var (
	errLargeTransaction = errors.New("transaction is too large")
)

// checkUnlockConditions looks at the UnlockConditions and verifies that all
// public keys are recognized. Unrecognized public keys are automatically
// accpeted as valid by the state, but rejected by the transaction pool. This
// allows new types of keys to be added via a softfork without alienating all
// of the older nodes.
func (tp *TransactionPool) checkUnlockConditions(uc consensus.UnlockConditions) error {
	for _, pk := range uc.PublicKeys {
		if pk.Algorithm != consensus.SignatureEntropy &&
			pk.Algorithm != consensus.SignatureEd25519 {
			return errors.New("unrecognized key type in transaction")
		}
	}

	return nil
}

// IsStandardTransaction enforces extra rules such as a transaction size limit.
// These rules can be altered without disrupting consensus.
func (tp *TransactionPool) IsStandardTransaction(t consensus.Transaction) (err error) {
	// Check that the size of the transaction does not exceed the standard
	// established in Standard.md. Larger transactions are a DOS vector,
	// because someone can fill a large transaction with a bunch of signatures
	// that require hashing the entire transaction. Several hundred megabytes
	// of hashing can be required of a verifier. Enforcing this rule makes it
	// more difficult for attackers to exploid this DOS vector, though a miner
	// with sufficient power could still create unfriendly blocks.
	if len(encoding.Marshal(t)) > TransactionSizeLimit {
		return errLargeTransaction
	}

	// Check that all public keys are of a recognized type. Need to check all
	// of the UnlockConditions, which currently can appear in 3 separate fields
	// of the transaction. Unrecognized types are ignored because a softfork
	// may make certain unrecognized signatures invalid, and this node cannot
	// tell which sigantures are the invalid ones.
	for _, sci := range t.SiacoinInputs {
		err = tp.checkUnlockConditions(sci.UnlockConditions)
		if err != nil {
			return
		}
	}
	for _, fct := range t.FileContractTerminations {
		err = tp.checkUnlockConditions(fct.TerminationConditions)
		if err != nil {
			return
		}
	}
	for _, sfi := range t.SiafundInputs {
		err = tp.checkUnlockConditions(sfi.UnlockConditions)
		if err != nil {
			return
		}
	}

	// Check that any file contracts do not start for at least
	// FileContractConfirmWindow blocks. By having a largeish confirm window,
	// the probability of a file contract making it into the unconfirmed set
	// but then becoming invalid before making the confirmed set is reduced.
	// The transaction pool is still stable if this happens, but 0-confirmation
	// hosts may respond poorly.
	for _, fc := range t.FileContracts {
		if fc.Start < tp.state.Height()+FileContractConfirmWindow {
			return errors.New("file contract cannot start so close to the current height")
		}
	}

	// Check that any terminations do not become invalid for at least
	// FileContractConfirmWindow blocks.
	for _, fct := range t.FileContractTerminations {
		// Because the validity of the transaction has not yet been checked,
		// there must be a check that the corresponding file contract
		// termination is in the unconfirmed set.
		fc, exists := tp.state.FileContract(fct.ParentID)
		if !exists {
			fc, exists = tp.fileContracts[fct.ParentID]
			if !exists {
				return errors.New("termination submitted for unknown file contract.")
			}
		}
		if fc.Start < tp.stateHeight-FileContractConfirmWindow {
			return errors.New("termination submitted too late")
		}
	}

	// Check that all arbitrary data is prefixed using the recognized set of
	// prefixes. The allowed prefixes include a 'NonSia' prefix for truly
	// arbitrary data. Blocking all other prefixes allows arbitrary data to be
	// used to orchestrate more complicated soft forks in the future without
	// putting older nodes at risk of violating the new rules.
	for _, data := range t.ArbitraryData {
		if !strings.HasPrefix(data, modules.PrefixHostAnnouncement) &&
			!strings.HasPrefix(data, PrefixNonSia) {
			return errors.New("arbitrary data contains unrecognized prefix")
		}
	}

	return
}
