package spec

type EnvoyServerSpec struct {
	Ip            string `yaml:"ip"`
	PortSsh       int    `yaml:"port.ssh" default:"22"`
	FilerPort     int    `yaml:"filer.port" default:"8888"`
	FilerGrpcPort int    `yaml:"filer.port.grpc" default:"18888"`
	S3Port        int    `yaml:"s3.port" default:"8333"`
	WebdavPort    int    `yaml:"webdav.port" default:"7333"`
	Version       string `yaml:"version,omitempty"`
}
