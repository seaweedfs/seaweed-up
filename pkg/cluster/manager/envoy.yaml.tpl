admin:
  access_log_path: {{.DataDir}}/admin_access.log
  address:
    socket_address: { address: 0.0.0.0, port_value: 9901 }

static_resources:
  listeners:
  - name: listener_0
    address:
      socket_address: { address: 0.0.0.0, port_value: 10000 }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http
          codec_type: AUTO
          route_config:
            name: seaweedfs_route
            virtual_hosts:
            - name: seaweedfs_host
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route: { cluster: seaweedfs_filers }
          http_filters:
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
  - name: seaweedfs_filers
    connect_timeout: 5s
    load_assignment:
      cluster_name: seaweedfs_filers
      endpoints:
      - lb_endpoints:
        {{- range .FilerEndPoints }}
        - endpoint:
            address:
              socket_address:
                address: {{.Ip}}
                port_value: {{.Port}}
        {{- end}}
