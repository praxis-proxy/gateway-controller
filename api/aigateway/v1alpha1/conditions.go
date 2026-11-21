package v1alpha1

// The resources in this group expose a single condition type in v1alpha1. The
// criteria for Ready differ per resource; see the readiness table in
// docs/design/api-v1alpha1.md.
const (
	// ConditionTypeReady reports whether a resource meets its readiness
	// criteria.
	ConditionTypeReady = "Ready"

	// ReasonReady indicates the resource meets its readiness criteria.
	ReasonReady = "Ready"

	// ReasonConflict indicates another resource won a contended claim.
	ReasonConflict = "Conflict"

	// ReasonInvalid indicates the resource's configuration is rejected by a
	// check the apiserver cannot make, such as a cross-resource constraint.
	ReasonInvalid = "Invalid"

	// ReasonDependencyNotReady indicates a resource this one depends on is
	// absent or not ready.
	ReasonDependencyNotReady = "DependencyNotReady"
)
