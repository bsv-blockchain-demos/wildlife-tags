package audit

import (
	"fmt"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// parseTx reads a stored transaction, accepting either raw or BEEF bytes.
func parseTx(raw []byte) (*transaction.Transaction, error) {
	if tx, err := transaction.NewTransactionFromBytes(raw); err == nil {
		return tx, nil
	}
	tx, err := transaction.NewTransactionFromBEEF(raw)
	if err != nil {
		return nil, fmt.Errorf("audit: parse transaction: %w", err)
	}
	return tx, nil
}
