package proc

// InstanceVariable carries, in the environment of every worker and check
// an Inkuna starts, the ID of the data directory it runs on. What those
// processes start inherits it, so after a crash the next Inkuna on that
// directory finds what the last one left running by it (EndLeftOver).
const InstanceVariable = "INKUNA_INSTANCE"

// Mark is the environment entry that marks a process as started by the
// Inkuna with instance ID id.
func Mark(id string) string {
	return InstanceVariable + "=" + id
}
