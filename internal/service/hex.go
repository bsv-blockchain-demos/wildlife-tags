package service

import "encoding/hex"

func hexOf(b []byte) string { return hex.EncodeToString(b) }
