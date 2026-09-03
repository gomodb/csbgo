# csbgo

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/gomodb/csbgo.svg)](https://pkg.go.dev/github.com/gomodb/csbgo)
[![Go Report Card](https://goreportcard.com/badge/github.com/gomodb/csbgo)](https://goreportcard.com/report/github.com/gomodb/csbgo)

一个纯 Go 标准库实现的 **CSB（云服务总线）HTTP 服务调用客户端**，无任何第三方依赖。

参考了阿里云 `csb-sdk/others/golang` 的调用实现，保留了其签名协议（HMAC-SHA1 + 字典序规范化），同时修复了原实现中的多处问题（JSON 判断写反、硬编码 `ResponseHeaderTimeout`、GET 请求携带 body、全局可变默认值等），并提供了更符合 Go 习惯、更易上手的 API。

## 安装

```bash
go get github.com/gomodb/csbgo
```

模块只依赖 Go 标准库，不引入任何第三方依赖。

## 签名协议（与原实现的兼容性）

CSB 签名 = `base64(HMAC-SHA1(secretKey, canonicalizedParams))`，其中 `canonicalizedParams` 是把以下参数合并后、按 key 的字典序（字节序）升序拼接成 `k1=v1&k2=v2&...` 得到：

- URL 中自带的 query 参数（原样，不做转义）
- 请求提供的 query 参数
- 请求提供的 form 参数
- 协议字段：`_api_name`、`_api_version`、`_api_timestamp`、`_api_access_key`

`_api_secret_key` 与 `_api_signature` 不会参与签名。签名结果通过 HTTP 头 `_api_signature` 传递，`_api_name` / `_api_version` / `_api_timestamp` / `_api_access_key` 也同时作为请求头发送。

## 快速开始

```go
package main

import (
 "context"
 "log"

 csbgo "github.com/gomodb/csbgo"
)

func main() {
 client := csbgo.New(
  csbgo.WithBaseURL("http://broker.example.com/CSB"), // CSB 唯一端点
  csbgo.WithAK("access-key"),
  csbgo.WithSK("secret-key"),
  csbgo.WithTimeout(10*time.Second),
  csbgo.WithDebug(true),
  csbgo.WithRetries(2),
 )

 resp, err := client.Do(context.Background(),
  csbgo.NewRequest(csbgo.MethodPost).
   WithAPI("MyService").
   WithVersion("v1").
   WithQuery("name", "wiseking"). // URL 查询参数（参与签名）
   WithForm("p1", "dog").         // form 参数（参与签名；POST 时作为请求体）
   WithHeader("X-Trace", "abc"),
 )
 if err != nil {
  // 含传输错误与非 2xx 状态错误
  log.Fatalf("call failed: %v", err)
 }
 log.Println(resp.String())
}
```

## 核心 API

### `New(opts ...Option) *Client`

创建一个可复用的客户端（并发安全）。

| Option | 作用 |
| --- | --- |
| `WithAK` / `WithSK` | 默认的 access key / secret key |
| `WithAPI` / `WithVersion` | 默认的 API 名 / 版本 |
| `WithBaseURL` | CSB 端点地址（必填，单个端点） |
| `WithTimeout` | 总超时时间 |
| `WithHTTPClient` | 传入自定义 `*http.Client`（代理 / TLS / 连接池等） |
| `WithTransport` | 传入自定义 `http.RoundTripper`（可用 `RoundTripFunc` 适配函数） |
| `WithUserAgent` | 自定义 User-Agent（默认 `csbBroker`） |
| `WithDebug` | 输出调试日志（默认打到 stderr） |
| `WithLogger` | 自定义日志输出（实现 `Printf` 即可，`*log.Logger` 天然满足） |
| `WithRetries` | 传输错误 / 5xx 的重试次数 |
| `WithStatusCheck` | 自定义状态码判定（默认仅接受 2xx，传 `nil` 关闭检查） |

### `NewRequest(method) *Request`

链式构建一次调用：`WithAPI` / `WithVersion` / `WithQuery` / `WithQueryInt` / `WithQueries` / `WithForm` / `WithForms` / `WithHeader` / `WithHeaders` / `WithBody` / `WithJSON`，URL 组合用 `Host` / `Path` / `Pathf`，以及按请求覆盖 `WithAK` / `WithSK` / `WithMethod` / `WithStatusCheck`。

`Request` 不带 URL：请求地址始终是 Client 的 `WithBaseURL` 端点。`Request` 可通过 `Clone()` 从一个“端点模板”派生单次请求而不影响原请求：

```go
base := csbgo.NewRequest(csbgo.MethodGet).WithAPI("MyService").WithVersion("v1")

r1 := base.Clone().Path("users/").Path("1")      // 读用户
r2 := base.Clone().Path("orders/").WithQueryInt("page", 2)
```

### `Client.Do(ctx, req) (*Response, error)`

发起调用。

- `error` 非空 = 本地失败（参数/URL/序列化/传输重试耗尽）**或**状态码检查失败。
- **默认只接受 2xx**：非 2xx 返回一个 `*StatusError`（`errors.As` 可取出，其 `Response` 字段携带完整状态码/响应头/响应体），方便读取 CSB 返回的错误详情。
- 用 `WithStatusCheck(nil)` 关闭检查（恢复为“任何响应都 nil error”），或用 `WithStatusCheck(csbgo.AcceptStatus(200, 201, 204))` 自定义。
- `Response` 提供 `String()`、`OK()`、`JSON(&v)` 便捷方法。

```go
resp, err := client.Do(ctx, req)
if err != nil {
 var se *csbgo.StatusError
 if errors.As(err, &se) {
  log.Printf("broker 返回 %d：%s", se.Response.StatusCode, se.Response.String())
 }
 return err
}
var out struct{ Code int `json:"code"` }
_ = resp.JSON(&out)
```

## 复用与并发

`Client` 在 `New` 之后不可变（`Do` 只读它），内部 `*http.Client` 本身并发安全，因此可以**初始化一次后设为包级变量**，供任意 goroutine / 业务代码复用：

```go
var csbClient = csbgo.New(
    csbgo.WithAK("ak"),
    csbgo.WithSK("sk"),
    csbgo.WithBaseURL("http://broker.example.com/"),
    csbgo.WithTimeout(10*time.Second),
)

func GetUser(ctx context.Context, id int) (*User, error) {
    resp, err := csbClient.Do(ctx,
        csbgo.NewRequest(csbgo.MethodGet).
            WithAPI("UserService").WithVersion("v1").
            Path("users/").Path(strconv.Itoa(id)),
    )
    // ...
}
```

- **Client 共享、Request 每次新建**（或从模板 `Clone()`）：不要把同一个 `*Request` 边改边并发用，`With*` 会原地修改它。
- 需要按租户/服务区分配不同 AK/SK 时，创建**多个** Client（每个凭证一个），而不是改全局。
- 已通过 `go test -race` 验证：单 Client + 请求模板 `Clone()` 在 64 个并发 goroutine 下无数据竞争。

## 参数发送规则

| 方法 | Query 参数 | Form 参数 | 请求体 |
| --- | --- | --- | --- |
| GET | 追加到 URL | 追加到 URL | 无 |
| POST（无显式 body） | 追加到 URL | `application/x-www-form-urlencoded` 请求体 | form 编码 |
| POST + `WithJSON` | 追加到 URL | `application/json` 请求体 | JSON |
| POST + `WithBody` | 追加到 URL | 自定义 Content-Type 请求体 | 原始字节 |

> 无论发送路径如何，query 与 form 参数都会一并进入签名，这与 CSB broker 对“参与签名参数”的要求一致。

## 运行测试

```bash
go test ./...
go test -race ./...
```

## License

[MIT](LICENSE)
