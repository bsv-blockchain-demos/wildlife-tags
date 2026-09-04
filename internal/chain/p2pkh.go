package chain

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

// errNotP2PKH is internal: callers treat a non-P2PKH output as "not ours"
// rather than as a failure.
var errNotP2PKH = errors.New("chain: not a p2pkh locking script")

// addressFromP2PKH decodes a standard payment output's address.
func addressFromP2PKH(lockingScript []byte, mainnet bool) (string, error) {
	s := script.Script(lockingScript)
	addr := p2pkh.Decode(&s, mainnet)
	if addr == nil {
		return "", errNotP2PKH
	}
	return addr.AddressString, nil
}

// DepositLockingScriptHex renders an address as the locking script a BRC-100
// wallet needs in order to pay it.
//
// BRC-100's createAction takes a script, not an address, so funding the program
// from a desktop wallet otherwise means hand-assembling five opcodes around a
// base58 decode -- which is exactly the kind of thing that silently sends money
// to an address nobody controls.
func DepositLockingScriptHex(address string, mainnet bool) (string, error) {
	addr, err := script.NewAddressFromString(address)
	if err != nil {
		return "", fmt.Errorf("chain: parse deposit address %s: %w", address, err)
	}
	// script.Address carries no network flag, so re-encode from the recovered
	// hash and compare. An address for the wrong network decodes fine and pays
	// somewhere nobody controls, which is a mistake worth catching here rather
	// than after the money has gone.
	check, err := script.NewAddressFromPublicKeyHash(addr.PublicKeyHash, mainnet)
	if err != nil {
		return "", fmt.Errorf("chain: re-encode deposit address: %w", err)
	}
	if check.AddressString != address {
		return "", fmt.Errorf("chain: address %s is not valid for this network (expected the form %s)", address, check.AddressString)
	}
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		return "", fmt.Errorf("chain: build deposit locking script: %w", err)
	}
	return hex.EncodeToString(lock.Bytes()), nil
}
