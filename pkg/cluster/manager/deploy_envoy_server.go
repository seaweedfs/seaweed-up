package manager

import (
	"bytes"
	_ "embed"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
	"github.com/thanhpk/randstr"
	"strings"
	"text/template"
)

//go:embed envoy.yaml.tpl
var envoyYamlTemplate string

func (m *Manager) DeployEnvoyServer(filerSpecs []*spec.FilerServerSpec, envoySpec *spec.EnvoyServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", envoySpec.Ip, envoySpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		var s3EndPoints, webdavEndPoints []*spec.FilerServerSpec
		for _, filerSpec := range filerSpecs {
			if filerSpec.PortGrpc == 0 {
				filerSpec.PortGrpc = filerSpec.Port + 10000
			}
			if filerSpec.S3 || filerSpec.S3Port != 0 {
				s3EndPoints = append(s3EndPoints, filerSpec)
			}
			if filerSpec.Webdav || filerSpec.WebdavPort != 0 {
				webdavEndPoints = append(webdavEndPoints, filerSpec)
			}
		}

		funcs := template.FuncMap{"join": strings.Join}
		envoyTmpl, err := template.New("envoy.yaml").Funcs(funcs).Parse(envoyYamlTemplate)
		if err != nil {
			return fmt.Errorf("parsing template: %v", err)
		}
		data := map[string]interface{}{
			"ConfigDir":            m.confDir,
			"DataDir":              m.dataDir,
			"HasFilerEndPoint":     len(filerSpecs) > 0 && envoySpec.FilerPort != 0,
			"HasFilerGrpcEndPoint": len(filerSpecs) > 0 && envoySpec.FilerGrpcPort != 0,
			"FilerEndPoints":       filerSpecs,
			"HasS3EndPoint":        len(s3EndPoints) > 0 && envoySpec.S3Port != 0,
			"S3EndPoints":          s3EndPoints,
			"HasWebdavEndPoint":    len(webdavEndPoints) > 0 && envoySpec.WebdavPort != 0,
			"WebdavEndPoints":      s3EndPoints,
			"Envoy":                envoySpec,
		}
		var buf bytes.Buffer
		if err := envoyTmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("generating template: %v", err)
		}

		component := "envoy"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.deployEnvoyInstance(op, component, componentInstance, envoySpec, &buf)

	})
}

func (m *Manager) deployEnvoyInstance(op operator.CommandOperator, component string, componentInstance string, envoySpec *spec.EnvoyServerSpec, buf *bytes.Buffer) error {
	info("Deploying " + componentInstance + "...")

	dir := "/tmp/seaweed-up." + randstr.String(6)

	defer op.Execute("rm -rf " + dir)

	err := op.Execute("mkdir -p " + dir + "/config")
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	if strings.HasPrefix(envoySpec.Version, "v") {
		envoySpec.Version = envoySpec.Version[1:]
	}

	data := map[string]interface{}{
		"Component":         component,
		"ComponentInstance": componentInstance,
		"ConfigDir":         m.confDir,
		"DataDir":           m.dataDir,
		"TmpDir":            dir,
		"SkipEnable":        m.skipEnable,
		"SkipStart":         m.skipStart,
		"Version":           envoySpec.Version,
	}

	installScript, err := scripts.RenderScript("install_envoy.sh", data)
	if err != nil {
		return err
	}

	err = op.Upload(installScript, fmt.Sprintf("%s/install_%s.sh", dir, componentInstance), "0755")
	if err != nil {
		return fmt.Errorf("error received during upload install script: %s", err)
	}

	err = op.Upload(buf, fmt.Sprintf("%s/config/%s.yaml", dir, component), "0755")
	if err != nil {
		return fmt.Errorf("error received during upload %s.yaml: %s", component, err)
	}

	info("Installing " + componentInstance + "...")
	err = op.Execute(fmt.Sprintf("cat %s/install_%s.sh | SUDO_PASS=\"%s\" sh -\n", dir, componentInstance, m.sudoPass))
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	info("Done.")
	return nil
}
