admin:
  access_log_path: {{.DataDir}}/admin_access.log
  address:
    socket_address: { address: 0.0.0.0, port_value: 9901 }

static_resources:
  listeners:
  {{- if .HasFilerEndPoint }}
  - name: listener_filer
    address:
      socket_address: { address: 0.0.0.0, port_value: {{.Envoy.FilerPort}} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http
          codec_type: AUTO
          route_config:
            name: seaweedfs_filer_route
            virtual_hosts:
            - name: seaweedfs_filer_host
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { cluster: seaweedfs_filer_cluster }
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  {{- end }}
  {{- if .HasFilerGrpcEndPoint }}
  - name: listener_filer_grpc
    address:
      socket_address: { address: 0.0.0.0, port_value: {{.Envoy.FilerGrpcPort}} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http
          stream_idle_timeout: 0s
          codec_type: AUTO
          route_config:
            name: seaweedfs_filer_grpc_route
            virtual_hosts:
            - name: seaweedfs_filer_grpc_host
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { timeout: 0s, cluster: seaweedfs_filer_grpc_cluster }
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  {{- end }}
  {{- if .HasS3EndPoint }}
  - name: listener_s3
    address:
      socket_address: { address: 0.0.0.0, port_value: {{.Envoy.S3Port}} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http
          codec_type: AUTO
          route_config:
            name: seaweedfs_s3_route
            virtual_hosts:
            - name: seaweedfs_s3_host
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { cluster: seaweedfs_s3_cluster }
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  {{- end }}
  {{- if .HasWebdavEndPoint }}
  - name: listener_webdav
    address:
      socket_address: { address: 0.0.0.0, port_value: {{.Envoy.WebdavPort}} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http
          codec_type: AUTO
          route_config:
            name: seaweedfs_webdav_route
            virtual_hosts:
            - name: seaweedfs_webdav_host
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { cluster: seaweedfs_webdav_cluster }
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  {{- end }}
  clusters:
  {{- if .HasFilerEndPoint }}
  - name: seaweedfs_filer_cluster
    connect_timeout: 5s
    load_assignment:
      cluster_name: seaweedfs_filer_cluster
      endpoints:
      - lb_endpoints:
        {{- range .FilerEndPoints }}
        - endpoint:
            address:
              socket_address:
                address: {{.Ip}}
                port_value: {{.Port}}
        {{- end}}
  {{- end }}
  {{- if .HasFilerGrpcEndPoint }}
  - name: seaweedfs_filer_grpc_cluster
    connect_timeout: 5s
    http2_protocol_options: {}
    load_assignment:
      cluster_name: seaweedfs_filer_grpc_cluster
      endpoints:
      - lb_endpoints:
        {{- range .FilerEndPoints }}
        - endpoint:
            address:
              socket_address:
                address: {{.Ip}}
                port_value: {{.PortGrpc}}
        {{- end}}
  {{- end }}
  {{- if .HasS3EndPoint }}
  - name: seaweedfs_s3_cluster
    connect_timeout: 5s
    load_assignment:
      cluster_name: seaweedfs_s3_cluster
      endpoints:
      - lb_endpoints:
        {{- range .S3EndPoints }}
        - endpoint:
            address:
              socket_address:
                address: {{.Ip}}
                port_value: {{.S3Port}}
        {{- end}}
  {{- end }}
  {{- if .HasWebdavEndPoint }}
  - name: seaweedfs_webdav_cluster
    connect_timeout: 5s
    load_assignment:
      cluster_name: seaweedfs_webdav_cluster
      endpoints:
      - lb_endpoints:
        {{- range .WebdavEndPoints }}
        - endpoint:
            address:
              socket_address:
                address: {{.Ip}}
                port_value: {{.WebdavPort}}
        {{- end}}
  {{- end }}
