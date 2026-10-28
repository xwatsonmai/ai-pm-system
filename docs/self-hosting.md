# 自部署

本文介绍如何把智驱部署到一台服务器上给团队使用，以 Docker Compose 加内置账号登录为例。只在本机试用的话，直接按 [README](../README.md#启动) 运行即可，不需要 Docker。

## 准备

- 构建机：Go 1.26.2+、Node.js 22.19+、npm 和 Git，用于运行测试和打包。
- 服务器：Linux x86_64，安装 Docker 和 Compose 插件；更新脚本还需要 `python3`（3.6+）、`sqlite3` 和 `rsync`。
- 网络：服务需要能访问项目的 Git 仓库和你配置的模型服务。

## 1. 构建发布包

在源码目录执行：

```sh
make test
./deploy/package.sh 20261027-v1
```

打包脚本会交叉编译 Linux amd64 二进制，生成 `bin/releases/<release>.tar.gz`。包里有二进制、Pi 运行时源文件、Dockerfile、Compose 文件、`update.py` 和 `SHA256SUMS`，不包含 `data/`、凭据或本机的 `node_modules`。

## 2. 在服务器上构建镜像

把压缩包上传到服务器，例如 `/data/ai-pm/releases/`，然后：

```sh
cd /data/ai-pm/releases
mkdir 20261027-v1 && tar -xzf 20261027-v1.tar.gz -C 20261027-v1
cd 20261027-v1
sha256sum -c SHA256SUMS
docker build -t ai-pm:20261027-v1 .
```

构建时按 lockfile 安装 Pi SDK 依赖，并用本地测试桩运行运行时测试，不会调用付费模型。服务器访问不到默认的 Debian 或 npm 源时，可以用构建参数换成镜像源：`--build-arg APT_MIRROR=… --build-arg APT_SECURITY_MIRROR=… --build-arg NPM_REGISTRY=…`；`NODE_IMAGE` 可以指定本机已有的 Node 22 镜像。

## 3. 准备目录和配置

```text
/data/ai-pm/
  compose.yaml          # 从发布包复制
  .env
  shared/auth.json      # 属主 1000:1000，权限 0600
  shared/data/          # 属主 1000:1000，权限 0700
```

容器以 UID/GID 1000 运行，根文件系统只读，数据只写入 `shared/data`。

`shared/auth.json` 使用内置账号：

```json
{
  "provider": "local",
  "public_url": "http://your-host:8090"
}
```

`public_url` 是浏览器实际访问的地址。服务挂在反向代理的子路径下时，把路径一起写上，例如 `https://example.com/workspace`，见下文。可选的 `platform_admin_login_ids` 和接入外部登录的方式见 [登录方式](login.md)。

`.env`：

```dotenv
AI_PM_IMAGE=ai-pm:20261027-v1
AI_PM_BIND_IP=0.0.0.0
```

`AI_PM_BIND_IP` 是宿主机上映射 8090 端口的地址，默认 `127.0.0.1`。要对外提供服务，填服务器 IP 或 `0.0.0.0`。

## 4. 启动和初始化

```sh
cd /data/ai-pm
docker compose up -d --wait
docker compose logs app | grep 初始化口令
```

打开 `public_url`，用日志里的初始化口令创建首位平台管理员。之后：

1. 在「平台设置 → 平台账号」为成员创建账号，并把用户名和初始密码告诉他们。
2. 在「平台设置 → 模型配置」添加模型，再选为平台默认模型。
3. 创建项目，连接 Git 仓库，分配项目成员。

检查服务：`curl --fail http://your-host:8090/api/health` 应返回 200，匿名访问 `/api/projects` 应返回 401。健康检查不带子路径，从任何地址访问都会响应，方便部署工具直接探测容器。

## HTTPS 和反向代理

服务本身只提供 HTTP。对外部网络开放时，建议放在 HTTPS 反向代理后面：

- `public_url` 填 `https://` 地址，登录 Cookie 会自动加上 Secure。
- 代理要原样转发 `Host` 头（例如 nginx 的 `proxy_set_header Host $host;`）。服务只接受本机地址和 `public_url` 主机的请求。
- 回答通过 SSE 实时推送，代理需要关闭响应缓冲（例如 `proxy_buffering off;`）。

### 挂在子路径下

服务可以挂在代理的某个路径下，例如 `https://example.com/workspace/`：

- `public_url` 写成 `https://example.com/workspace`（不带结尾斜杠）。登录回调、MCP 地址和 OAuth 发现地址都会带上这个路径；接入外部登录时，回调要按新地址登记。
- 代理原样转发完整路径，不要去掉前缀，由服务自己处理。访问 `/workspace` 会跳到 `/workspace/`。
- 登录 Cookie 只对这个路径生效，不会和同一域名下的其他应用混用。
- 需求正文里的附件链接按 `/api/...` 保存，页面显示时自动加上路径，所以以后改挂载位置不需要改数据。
- MCP 客户端按 OAuth 规范发现授权服务器时，会先访问站点根下的 `/.well-known/...`，代理返回 404 即可；客户端随后会在 `/workspace/.well-known/openid-configuration` 找到元数据。

nginx 示例：

```nginx
location ^~ /workspace/ {
    proxy_pass http://127.0.0.1:8090;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_read_timeout 1h;
    client_max_body_size 12m;  # 附件上限 10 MB
}
location = /workspace { return 302 /workspace/; }
```

`proxy_pass` 后面不要加路径，这样转发时会保留 `/workspace/` 前缀。

## 更新

发布包里的 `update.py` 分两步更新：先在服务运行时准备，再短暂停服切换。

```sh
cd /data/ai-pm
python3 releases/20261028-v2/update.py prepare --release 20261028-v2 --image ai-pm:20261028-v2 \
  --health-url http://your-host:8090/api/health
python3 releases/20261028-v2/update.py apply --release 20261028-v2 \
  --health-url http://your-host:8090/api/health
```

- `prepare` 用 SQLite 在线备份生成一致的数据库快照，连同配置打包到 `backups/pre-<release>.tar.gz` 并校验。有 Agent 分析、需求评审或续答任务正在运行时，它会拒绝执行。
- `apply` 需要在 `prepare` 之后 15 分钟内执行。它停止旧容器、启动新镜像，每 100ms 探测一次健康检查；新版本没能恢复时，会自动切回旧镜像，并保留当前数据。
- 手动回退：把 `releases/<release>/env.before` 复制为 `.env`，再执行 `docker compose up -d`。

这个流程只适用于数据格式兼容的更新。包含不兼容迁移的版本，需要单独安排迁移和恢复方案。

## 备份

`shared/` 下的内容都需要备份，其中 `shared/data/repository.key` 是仓库凭据的加密主密钥，缺了它就无法解密已保存的仓库凭据。服务运行时不要直接复制 `ai-pm.db`，应该用 `sqlite3 ai-pm.db ".backup backup.db"`，或者用上面的 `update.py prepare` 生成快照。

## 可选：隔离源码阅读

项目可以启用「隔离源码阅读」，让平台 Agent 在独立容器里只读访问指定版本的源码。这需要额外安装受限 runner 和 Worker 镜像，并启用 `compose.sandbox.yaml`，见 [项目源码只读沙盒](readonly-sandbox.md)。
