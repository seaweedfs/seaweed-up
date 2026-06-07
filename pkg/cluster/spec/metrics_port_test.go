package spec

import (
	"bytes"
	"strings"
	"testing"
)

// metricsPort must reach the rendered weed options for master, volume and
// filer (it previously only did for s3), so Prometheus can scrape them.
func TestWriteToBuffer_MetricsPort(t *testing.T) {
	masters := []string{"10.0.0.1:9333"}

	t.Run("master emits when set, omits when zero", func(t *testing.T) {
		var set, unset bytes.Buffer
		(&MasterServerSpec{Ip: "10.0.0.1", MetricsPort: 9324}).WriteToBuffer(masters, &set)
		(&MasterServerSpec{Ip: "10.0.0.1"}).WriteToBuffer(masters, &unset)
		if !strings.Contains(set.String(), "metricsPort=9324") {
			t.Errorf("master: missing metricsPort=9324\n%s", set.String())
		}
		if strings.Contains(unset.String(), "metricsPort") {
			t.Errorf("master: should omit metricsPort when unset\n%s", unset.String())
		}
	})

	t.Run("volume emits when set", func(t *testing.T) {
		var b bytes.Buffer
		(&VolumeServerSpec{Ip: "10.0.0.1", MetricsPort: 9325}).WriteToBuffer(masters, &b)
		if !strings.Contains(b.String(), "metricsPort=9325") {
			t.Errorf("volume: missing metricsPort=9325\n%s", b.String())
		}
	})

	t.Run("filer emits when set", func(t *testing.T) {
		var b bytes.Buffer
		(&FilerServerSpec{Ip: "10.0.0.1", MetricsPort: 9327}).WriteToBuffer(masters, &b)
		if !strings.Contains(b.String(), "metricsPort=9327") {
			t.Errorf("filer: missing metricsPort=9327\n%s", b.String())
		}
	})
}
