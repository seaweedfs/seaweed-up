package filer

import "strings"

// TiKV stores filer metadata in a TiKV cluster using the tikv driver
// shipped with SeaweedFS.
type TiKV struct {
	// PdAddrs is the list of PD (placement driver) endpoints.
	PdAddrs []string `filer:"pdaddrs"`
	// DeleteRangeConcurrency bounds concurrent delete-range operations.
	// Optional.
	DeleteRangeConcurrency int `filer:"deleterange_concurrency"`
}

const tikvTemplate = `[tikv]
enabled = true
pdaddrs = [{{.AddrsLiteral}}]
deleterange_concurrency = {{.DeleteRangeConcurrency}}
`

type tikvTmplData struct {
	*TiKV
	AddrsLiteral string
}

func (t *TiKV) applyDefaults() {
	if t.DeleteRangeConcurrency == 0 {
		t.DeleteRangeConcurrency = 1
	}
}

// Name returns the canonical backend type name.
func (t *TiKV) Name() string { return "tikv" }

// Validate ensures at least one PD address is supplied.
func (t *TiKV) Validate() error {
	if len(t.PdAddrs) == 0 {
		return requiredErr("tikv", "pdaddrs")
	}
	return nil
}

// RenderTOML renders the tikv section of filer.toml.
func (t *TiKV) RenderTOML() (string, error) {
	quoted := make([]string, 0, len(t.PdAddrs))
	for _, addr := range t.PdAddrs {
		quoted = append(quoted, "\""+addr+"\"")
	}
	return render("tikv", tikvTemplate, tikvTmplData{
		TiKV:         t,
		AddrsLiteral: strings.Join(quoted, ", "),
	})
}
