# 登录方式

工作台支持三种登录模式，由数据目录下的 `auth.json`（或 `-auth-config` 指定的文件）决定。修改配置后需重启服务。

| 模式 | 配置 | 适用场景 |
| --- | --- | --- |
| 本机免登录 | 不放 `auth.json` | 单人在本机试用；只允许监听 `127.0.0.1` 等回环地址 |
| 内置账号 | `"provider": "local"` | 自行部署给团队使用，不依赖外部身份系统 |
| 外部登录扩展 | `"provider": "<名称>"` | 接入企业已有的身份认证服务（见下文「扩展边界」） |

配置文件存在但内容无效、`provider` 不可用，或扩展初始化失败时，服务启动会报错，**不会**退回免登录模式。

## 内置账号

```json
{
  "provider": "local",
  "public_url": "http://your-host:8090",
  "platform_admin_login_ids": []
}
```

- `public_url`：浏览器实际访问的站点根地址，不含路径。为 HTTPS 地址时 Cookie 自动加 Secure。
- `platform_admin_login_ids`：可选。这里列出的用户名登录后自动成为平台管理员，且只能通过修改配置撤销。
- 不接受其他字段，拼写错误会在启动时报出。

### 首次初始化

还没有任何账号时，服务启动日志会打印一次性**初始化口令**：

```
尚无平台账号：打开访问地址，用初始化口令 <口令> 创建首位平台管理员（口令每次启动更新，用后失效）
```

打开工作台会进入「初始化平台」页面，填写口令、用户名和密码，即可创建首位平台管理员并登录。口令只保存在内存里，每次重启都会重新生成。创建完成后口令立即失效，之后启动也不会再打印。由于只有能看到服务日志的人才拿得到口令，即使服务监听在内网地址上，也不会被他人抢先初始化。

### 账号管理

平台管理员在「平台设置 → 平台账号」中管理账号：

- **新建**：填写用户名（2-32 位小写字母、数字或 `. _ -`，不区分大小写）、姓名和初始密码。界面会随机生成初始密码。成员首次登录时必须先改成自己的密码，改密前不能访问业务接口。
- **重置密码**：设置新的初始密码。成员已有的登录会话和个人 MCP 授权立即失效（视为旧密码可能已泄露），下次登录时需要再次改密，再重新授权客户端。
- **停用 / 启用**：停用会在同一事务里删除该成员的全部登录会话、个人 MCP 授权和未兑换的授权码；项目成员关系保留，重新启用后恢复。管理员不能停用自己的账号。

每位成员都可以在右上角「改密」修改自己的密码。修改后，其他设备上的登录会失效，已授权的 MCP 客户端保留。改名不会让成员退出登录。

项目成员和平台管理员从「平台账号」目录中选择，已停用的账号不能被新加入项目。

### 安全规则

- 密码长度为 8-128 位，使用 argon2id（t=3、m=64 MiB、p=2）加盐保存，参数随哈希一起存储，以后可以调高。同时进行的哈希计算最多 2 个，内存占用有上限。
- 输错 5 次密码后，该账号暂停登录 15 分钟（计数在内存中，重启清零）。每次尝试在计算哈希前就计入次数，并发请求也无法绕过上限。不存在的用户名同样会做一次哈希计算，避免通过响应时间判断用户名是否存在。但账号进入暂停期后会提示「次数过多」，据此仍能确认该用户名存在；成员用户名在项目内本就可见，因此接受这一点。
- 签发会话后会再次核对账号：如果管理员在校验密码期间停用了账号或重置了密码，这次登录作废。
- 会话有效期 12 小时。会话凭据在 SQLite 中只保存 SHA-256 摘要，Cookie 设置为 HttpOnly、SameSite=Lax。
- 登录、初始化、改密接口与其他写接口一样受跨站请求保护，来自其他站点的请求会被拒绝。
- 接口和页面都不会返回密码哈希。

### 接口

| 接口 | 说明 |
| --- | --- |
| `GET /api/auth/session` | 返回 `login: "password"`、`setup_required`、`must_change_password`，以及目录信息 `directory` |
| `POST /api/auth/setup` | `{token, username, name, password}`，创建首位管理员并登录 |
| `POST /api/auth/login` | `{username, password, return_to}`，`return_to` 只接受 `/oauth/authorize?...` |
| `POST /api/auth/password` | `{current_password, new_password, return_to}`，改密后签发新会话 |
| `GET/POST /api/platform/accounts`、`PATCH /api/platform/accounts/{user_id}` | 平台管理员列出、新建、修改账号（`name` / `password` / `disabled`） |

开发 MCP 的个人 OAuth 与登录方式无关：客户端授权时浏览器若未登录，会先跳到工作台登录（内置账号模式下回到登录页，并带上 `return_to`），登录或改密完成后再回到授权确认页。

## 扩展边界

登录核心在 `internal/auth`，包括会话、内置账号和 provider 注册表。外部登录以 provider 的形式在启动时注册：

```go
auth.Register("name", func(ctx context.Context, f *auth.File, store storage.Store) (auth.Login, error) { ... })
```

provider 返回 `auth.Login`：

- `Redirect`：跳转式登录，实现 `AccountLabel`、`Begin`、`Complete`。核心负责校验 `return_to`、强制跳转到 `public_url` 所在的主机，以及签发会话。`Complete` 返回 `auth.Failure` 错误码时，页面会显示对应提示。
- `Directory`：可选的成员目录，实现 `DirectoryLabel`、`Syncable`、`SyncDirectory`。不提供时，加成员从平台已有用户中选择。
- `Fingerprint`：身份来源的标识（例如 issuer 与 client_id），与 `public_url` 一起决定会话命名空间，任一变化后旧会话全部失效。provider 名称不参与计算，因此给旧配置补上 `"provider"` 不会让用户退出登录。

provider 用 `f.Decode(&cfg)` 严格解析自己的配置，`cfg` 应嵌入 `auth.Settings`。

### 私有扩展

外部登录扩展不必放进本仓库。私有部署可以维护一个跟随本仓库的下游仓库，只**新增**文件：在 `internal/` 下加入 provider 包，再在 `cmd/server/` 下加一个文件，在 `init()` 里调用 `auth.Register`。只要不修改本仓库已有的文件，同步上游时就不会产生冲突。

`auth.Register("", ...)` 可以让一个扩展接管未填写 `provider` 的配置文件，适合兼容在 provider 机制出现之前写好的 `auth.json`。
