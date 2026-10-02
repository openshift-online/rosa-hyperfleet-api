package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DoorbellChannel returns the LISTEN/NOTIFY channel name for a GVK.
// PostgreSQL limits identifiers to 63 bytes, so we hash long GVK strings.
func DoorbellChannel(gvk string) string {
	raw := fmt.Sprintf("resource_changes_%s", gvk)
	if len(raw) <= 63 {
		return raw
	}
	h := sha256.Sum256([]byte(gvk))
	return fmt.Sprintf("rc_%s", hex.EncodeToString(h[:12]))
}

type Resource struct {
	GVK               string
	Namespace         string
	Name              string
	UID               uuid.UUID
	TxidStamp         uint64
	ObjectVersion     int64
	Spec              json.RawMessage
	Status            json.RawMessage
	Metadata          json.RawMessage
	DeletionTimestamp *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type WriteRequest struct {
	GVK               string
	Namespace         string
	Name              string
	Spec              json.RawMessage
	Status            json.RawMessage
	Metadata          json.RawMessage
	DeletionTimestamp *time.Time
	ExpectedVersion   int64      // 0 for create, >0 for update
	ExpectedUID       *uuid.UUID // UID precondition for writes to an existing object
	ForceWrite        bool       // skip no-op suppression; default false = suppress content-equal writes
}

type StatusWriteRequest struct {
	GVK             string
	Namespace       string
	Name            string
	Status          json.RawMessage
	ExpectedVersion int64
	ExpectedUID     *uuid.UUID // UID precondition for writes to an existing object
	ForceWrite      bool       // skip no-op suppression; default false = suppress content-equal writes
}

type ObjectWriteRequest struct {
	GVK               string
	Namespace         string
	Name              string
	Spec              json.RawMessage
	Metadata          json.RawMessage
	DeletionTimestamp *time.Time
	ExpectedVersion   int64
	ExpectedUID       *uuid.UUID // UID precondition for writes to an existing object
	ForceWrite        bool
}

type WriteResult struct {
	Txid          uint64
	ObjectVersion int64
	UID           uuid.UUID
	Changed       bool // false when suppressed (content-equal write)
}
