package kb

import "strings"

// The concept types the server itself keys on (D853). A page of one of these
// types is found, gated or checked by its type, so lint must not call the type
// unknown just because the KB declares no template for it. Each match site
// uses the predicate here, so how a type is matched (exactly, or in any case)
// is decided in one place.
const (
	// SourceType is the conventional concept type of a source-ledger entry
	// (D278), matched exactly.
	SourceType = "Source"
	// IngestPlanType and IngestUnitType are the conventional types of an ingest
	// campaign and of one unit of its work (D1201), matched exactly.
	IngestPlanType = "IngestPlan"
	IngestUnitType = "IngestUnit"
	// ContradictionType is the type the gate and the governance tools count,
	// matched exactly.
	ContradictionType = "Contradiction"
	// ServiceType is the reserved type of a service page, matched in any case
	// (D158).
	ServiceType = "Service"
	// DigestType is the type link_to_retired keys on, matched in any case
	// (D322).
	DigestType = "digest"
)

// IsSourceType reports whether t is exactly SourceType.
func IsSourceType(t string) bool { return t == SourceType }

// IsIngestPlanType reports whether t is exactly IngestPlanType.
func IsIngestPlanType(t string) bool { return t == IngestPlanType }

// IsIngestUnitType reports whether t is exactly IngestUnitType.
func IsIngestUnitType(t string) bool { return t == IngestUnitType }

// IsContradictionType reports whether t is exactly ContradictionType.
func IsContradictionType(t string) bool { return t == ContradictionType }

// IsServiceType reports whether t is "Service" in any case.
func IsServiceType(t string) bool { return strings.EqualFold(t, ServiceType) }

// IsDigestType reports whether t is "digest" in any case.
func IsDigestType(t string) bool { return strings.EqualFold(t, DigestType) }

// IsKeyedType reports whether t is one of the types the server keys on, each
// matched the way its own predicate does.
func IsKeyedType(t string) bool {
	return IsSourceType(t) || IsContradictionType(t) || IsServiceType(t) || IsDigestType(t) ||
		IsIngestPlanType(t) || IsIngestUnitType(t)
}
