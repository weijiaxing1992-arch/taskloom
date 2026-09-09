# 部署、升级与回滚流程

## 交付物与前置条件

先阅读验收报告中的保留项。源码包包含 Web、Go、Flutter 源码及门户资源；服务包包含 server、deployment-preflight、凭据管理工具、web 与部署模板。不包含真实业务库、用户清单、密码、机器人密钥、.env、依赖缓存或私有运行日志。请由数据责任人单独受控交付数据库与密钥。

Linux amd64 对应 x86_64 ECS，不能在 ARM 实例上直接执行。macOS arm64 服务程序用于开发检查，不是公证 DMG。依赖版本以 go.mod、pnpm-lock.yaml、Flutter pubspec.lock 为准。先校验 SHA256SUMS.txt。

## 目录与端口

```text
/opt/devflow/releases/<version>/   server、deployment-preflight、web、脚本
/opt/devflow/current              指向当前 release 的符号链接
/opt/devflow/data/devflow.db      独立数据卷，devflow 用户读写
/opt/devflow/secrets/devflow.env  root:root 0600，systemd 读取
/opt/devflow/backups/            一致性快照，另行上传私有 OSS
443 / 80                        Caddy HTTPS / 跳转
127.0.0.1:19080                  Go API + Web，仅本机
```

安全组不开放 19080，不开放数据库端口；SSH 只允许运维来源。仅把 web/ 配给 DEVFLOW_WEB_DIR，绝不把整个交付目录作为站点根目录。

## 首次安装

1. 准备建议的 ECS、磁盘挂载、系统更新、时间同步、专用 devflow 系统用户。安装官方 Caddy；不要使用 root 运行 API。
2. 解压 linux-amd64 包到新 release 目录，核对文件校验和及 file server 的架构结果，保留原发布版本。
3. 从受控渠道取得一致性业务库快照，复制到尚未运行服务的 data 目录，并设置 devflow 所有、文件 0600 / 目录 0700。不要直接复制运行中的 DB/WAL，不要用旧系统 SQLite 操作现有库。
4. 全新企业初始化与示例清理要在隔离环境明确审核；不得直接用空数据库启动正式 server，因为直接启动可能生成演示数据。详细预置步骤参见 docs/deployment-preflight.md 的离线副本流程及工具 --help。
5. 将 deploy/devflow.env.example 安装为 secrets/devflow.env。设置真实绝对路径、回环监听、COOKIE_SECURE=true、SQLITE_SYNCHRONOUS=FULL，生成独立高强度 SESSION_SECRET。密钥只写入受控文件，不写进命令历史 / 文档 / Git。
6. 外部企微保持 mock 直到真实通道通过受控联调；上线须核查实际发送模式、出站限制和密钥文件。AI、MCP Token 按最小权限另行配置。
7. 建立 current 链接；复制 deploy/devflow.service 到 systemd，确认 ExecStart、User、ReadWritePaths、env 路径；daemon-reload 后 enable --now devflow。start-production.sh 会先运行环境 / 数据库预检，不满足时拒绝启动。
8. 将 deploy/Caddyfile.example 域名换成正式域名。配置 DNS、TLS、请求体上限与代理超时，验证配置后加载；公网门户地址与 backendUrl 在 portal/config.json 配置并重新构建。
9. 部署备份 service / timer，确认备份目录可写、OSS 上传脚本具有最小 RAM 权限；定时任务本身不等于异地灾备。
10. 验证 /api/health、登录、关键读写、上传下载、通知、水印真实 IP、权限隔离、进程重启、备份恢复和外部告警。全部通过后再对业务开放。

示例管理命令（密钥配置由受控 env 文件提供；这些命令不会创建数据库）：

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now devflow
sudo systemctl status devflow --no-pager
curl --fail http://127.0.0.1:19080/api/health
sudo journalctl -u devflow -n 100 --no-pager
```

## 备份与恢复演练

使用与服务相同 SQLite 引擎编译的 deployment-preflight：

```sh
/opt/devflow/current/deployment-preflight --db /opt/devflow/data/devflow.db --backup-out /opt/devflow/backups/new-snapshot.db
```

输出必须是一个不存在的新文件；备份按工具检查结果验收，记录 SHA-256、时间、库大小、恢复所需密钥标识（不记密钥值）。上传私有 OSS 后校验远端校验和。保留策略经业务批准后启用，严禁通配符删除正在使用的数据库。

恢复时停止写入；在新目录恢复快照和匹配密钥，运行预检、完整性和外键检查，隔离端口启动新服务抽测。确认正常后切换服务，保留故障库及 WAL 给调查使用。不要把备份覆盖到运行中数据库；恢复结果未经演练不能承诺 RTO。

## 升级与回滚

1. 维护窗口告知用户保存编辑；保留旧 release、env 和当前一致性备份。
2. 在离线快照先验证新迁移。发布目录必须不可变，不覆盖当前二进制或正在被页面引用的资源。
3. 使用 scripts/retain-web-assets.mjs 保留上一版带 hash 静态资源，避免旧页面动态加载失败。
4. 在维护窗口停止旧进程、切 current、启动新进程，检查健康、登录和关键链路。当前服务停止可能中断正在处理的请求，需预留窗口，不能承诺零停机。
5. 只有确认数据库模式向后兼容时才能只回滚二进制；不兼容时应停写并用匹配版本快照恢复到新数据路径。快照后的新增数据需要人工评估和回补。
6. 稳定后恢复开放。回滚不是随意删除 WAL 或反向执行迁移。

## 从源码编译

```sh
corepack pnpm install --frozen-lockfile
pnpm test
pnpm build
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o server ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o deployment-preflight ./cmd/deployment-preflight
```

需 Node >=22.12、锁定的 pnpm 和 go.mod 指定的 Go 工具链。源码包保留门户的已校验测试 DMG，因为当前门户构建会核对真实下载文件摘要；不能用占位下载替换。Flutter 构建需要 macOS、Xcode 和匹配 Flutter SDK；原生功能完整验收另行进行。

## 上线签署清单

必须由负责人记录：正式域名 / 地域、业务峰值、附件预算、RAM / 安全组、HTTPS、数据迁移审核、临时密码轮换、真实通知 / AI 联调、目标 ECS 压测、备份恢复演练、值班告警、业务 UAT 结果和回滚负责人。本地验收不能替代这些上线动作。

## 本机验收服务的运行位置

本机端口仍为 127.0.0.1:19086，由 com.devflow.local19086 用户 LaunchAgent 托管。为了避免 macOS 后台进程读取文稿目录受阻，运行目录改为当前用户 Library/Application Support/DevFlowLocal；实际业务库是其中 data/accepted-20260906.db，匹配密钥独立保存在 data/devflow.db.wecom-key。旧工作区 work/current-preview.db 保留为迁移前快照，不再作为活动库；不要在两个路径同时启动写入服务。

start-local-service.sh 在每次启动前检查绝对路径、非空文件和数据库预检，失败立即退出，不创建演示库。应用数据目录、密钥和本机用户专用 LaunchAgent 不放入公共交付包。本机使用回环 Cookie 与开发签名配置，不能直接照搬到公网；阿里云必须采用前述生产部署配置。macOS 退出当前用户后该用户服务不可视为全天候服务器，应使用 ECS 正式承载业务。
