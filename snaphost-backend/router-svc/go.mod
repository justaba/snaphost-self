module snaphost/router-svc

go 1.25.0

require (
	github.com/yandex-cloud/go-genproto v0.78.0
	github.com/yandex-cloud/go-sdk v0.31.0
	go.uber.org/zap v1.27.0
	snaphost/shared v0.0.0
)

require (
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.1 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20241219192143-6b3ec007d9bb // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241219192143-6b3ec007d9bb // indirect
	google.golang.org/grpc v1.69.2 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

replace snaphost/shared => ../shared
