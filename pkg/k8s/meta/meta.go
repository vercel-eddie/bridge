package meta

const (
	// LabelBridgeType identifies bridge resource types (e.g., "proxy").
	LabelBridgeType = "vercel.sh/bridge-type"

	// LabelManagedBy identifies resources managed by the bridge administrator.
	LabelManagedBy = "vercel.sh/bridge-managed-by"

	// LabelDeviceID stores the device KSUID on the namespace.
	LabelDeviceID = "vercel.sh/bridge-device-id"

	// LabelWorkloadSource stores the name of the source deployment that was cloned.
	LabelWorkloadSource = "vercel.sh/bridge-workload-source"

	// LabelWorkloadSourceNamespace stores the namespace of the source deployment.
	LabelWorkloadSourceNamespace = "vercel.sh/bridge-workload-source-ns"

	// BridgeTypeProxy is the label value for bridge proxy resources.
	BridgeTypeProxy = "proxy"

	// ManagedByAdministrator is the value for the managed-by label.
	ManagedByAdministrator = "administrator"

	// ProxySelector is the label selector string for bridge proxy resources.
	ProxySelector = LabelBridgeType + "=" + BridgeTypeProxy
)
