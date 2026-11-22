//go:build linux

package exec

import "testing"

// A step cannot reach the cloud metadata service through the guest's network.
//
// The userspace stack opens a step's connections as this process, so on a cloud
// VM a step dialling 169.254.169.254 would be the host asking - and IMDSv2's hop
// limit, which stops a container behind a bridge, would not stop it.
// gvisor-tap-vsock refuses link-local destinations unless Ec2MetadataAccess is
// set; this keeps it unset on purpose.
func TestAStepCannotReachTheMetadataService(t *testing.T) {
	t.Parallel()

	if userNetConfig().Ec2MetadataAccess {
		t.Error("the guest's network reaches link-local addresses, the metadata service among them")
	}
}
