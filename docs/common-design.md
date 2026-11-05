# v2 公共系统设计

本文规定共享基础能力与技术约束。[系统设计](design.md)规定检验业务模型，[API](api.md)规定请求与响应。本文描述目标设计，不声明现有工程已达到这些要求。

## 1. 系统组成与技术边界

采用 Go、Gin、database/sql + sqlx 的模块化单体。SQLite 用于开发和单机运行，PostgreSQL 用于服务器部署；两库提供相同业务语义。文档与后端同属一个仓库。前端通过 HTTP JSON 对接，不依赖指定的前端框架。

| 模块 | 职责 | 边界 |
|---|---|---|
| HTTP 与中间件 | 请求解析、认证、错误包装、CORS、请求关联 | 不复制一套领域规则 |
| 账户与会话 | 本地登录、密码、账户启停、管理员管理 | 不把纸面人员绑定到账号 |
| 字典与参考资料 | 工序、单位、样品维度、参考部件号 | 不规定生产路线，不作为样品准入表 |
| 样品解析 | 原文辅助拆解、人工确认映射、缓存 | 不自动修改检验单和生产批身份 |
| 检验台账 | 身份归组、内容保存、版本、取消、填报状态 | 不建立跨工序实物流向 |
| 查询与统计 | 一致版本集合、计数、缺陷、数量变化、报告 | 不从工序差额推库存或损耗 |
| 数据访问 | 事务、锁、索引、数据库方言、结构迁移 | 不以数据库异常为理由放过业务约束 |

业务规则只在服务层集中实现，单笔/批量、正常/抽检、两库均调用相同规则。HTTP handler 负责协议转换；SQL 参数化，动态字段、排序和分组表达式均从白名单映射。

资源 ID 使用不透明稳定字符串。调用方不能从 ID 的前缀、时间或流水猜测业务身份。对象版本是整数并发令牌；内容版本号用于历史排序；二者与纸面编号分开。

## 2. 用户与会话

### 2.1 用户数据

用户保留稳定 ID、唯一登录名、显示名、可空工号、可空所属单位、管理员标记、账户类型、启停状态、密码哈希及系统时间。登录名日常不可修改；停用保留历史，不提供删除用户以抹掉业务证据的入口。

管理员标记用于账户管理，账户类型用于填报类型边界。权限判断集中在可替换服务中，不建立多角色/权限点系统。业务证据记录操作者 ID 和当时显示名快照，后续改名或停用不改历史。

### 2.2 密码与会话机制

密码用 Argon2id，PHC 格式保存算法、参数和盐；不保存明文或可逆密文。参数可升级，旧哈希仍按自身参数验证。会话令牌使用至少 256 bit 安全随机数，服务端只保存令牌 SHA-256 哈希。

| 参数 | 设计默认值 | 说明 |
|---|---|---|
| SESSION_TTL | 12h | 会话绝对期限 |
| SESSION_IDLE_TTL | 2h | 空闲期限，活动不得突破绝对期限 |
| MAX_LOGIN_FAIL | 5 | 连续失败达到此值锁定 |
| LOCK_DURATION | 10m | 账户锁定时长 |
| COOKIE_NAME | sid | 会话 Cookie 名称 |
| COOKIE_SECURE | 本地 HTTP 可关闭；HTTPS 必须开启 | 不将开发默认视为生产配置 |

同时支持 HttpOnly Cookie 与 Bearer token。令牌并存和密码输入策略的精确协议在 API 中定义。退出撤销当前会话；本人改密、管理员重置密码、停用账户撤销该用户全部会话。账户类型变化还须清除此用户业务幂等结果，不能靠旧响应绕过新权限。每次请求检查会话和当前账户状态，业务写事务内再次校验。

管理员引导仅在用户库为空时执行。重启、种子初始化或迁移不得覆盖已有密码。不得在仓库、示例、镜像或日志中留下可用的默认生产凭据。

## 3. 字典、单位和模板

字典项用 category + code 唯一识别，并保留稳定 ID、label、sort_no、active、meta。code 与中文 label 分开；改 label 不改变关联 ID。meta 的 API 类型固定为对象或 null，不让客户端猜测它是对象还是 JSON 字符串。

工序类别固定为 inspection_operation；样品类别及初始候选值见[参考资料](reference-data.md)。计量单位独立且固定为“片”，目录只读，不与用户部门或工序字典混用。字典已有 ID 可以在历史中保留，停用表示不再提供给新建选择，不删除已保存文字和引用。

工序目录决定工序身份，模板决定展示布局：有效工序没有专用模板时使用通用模板，不因此拒绝录入。系统不规定工序先后、必经工序或每个生产批应有多少道工序。新工序通过共享目录登记，不在保存检验单时用名称悄悄新建。

模板提供完整纸面栏目、样品字段、机台/批号位置、计数和 35×3 缺陷。当前各工序共用全部栏目，不凭名称裁剪。template_version 与每个内容版本一起保存；历史用当时的字段编码和模板解释，不能按今天的标签重写历史。

新增字典取值、标签、排序和别名属于目录维护；新增持久化字段/缺陷编码、改变统计含义须同时变更字段目录、校验、存储和 API，不能声称任意结构变化都只需配置。

调用方在登录后或进入录入页时刷新目录，手动刷新可立即获取变更；不承诺永久缓存。若以后增加 ETag/版本协商，应单独扩展接口。历史显示优先使用内容中保留的文字快照。

## 4. 样品辅助解析

### 4.1 原文、结构化内容与身份分开

sample_raw 保留原始输入，核心五项用于查询和汇总，sample_identity 用于生产批身份；三者不互相自动覆盖。辅助解析、字典更新、缓存确认都不得改变已保存的单据。修改原文本身须显式保存为新版本。

### 4.2 解析流程

1. 保留输入原文，生成单独的匹配键，处理全半角、大小写、空白和分隔符。
2. 依据字典 code/aliases 进行位置无关的 token 匹配，优先识别已定义组合。
3. 返回核心五项、未知 token、冲突候选和辅助属性；没有可靠证据的值保持 null。
4. 按已识别维度查参考资料，返回零个或多个候选。多个候选不能假装唯一命中。
5. 使用人工确认缓存优先补充结构化候选；标明来源、确认人和时间。
6. 返回置信度及原因，供界面提示，不作为保存资格或质量合格率。

raw_only 表示没有核心字段；partial 表示五项不齐；no_drawing 表示核心字段齐而无参考项；唯一匹配且五项齐时原因 null。匹配到图号但缺项目仍为 partial。原文不在参考目录内属于正常情况。

### 4.3 缓存边界

缓存按规范化键记录原文示例、完整结构化映射、人工确认标记、字典/解析器版本及更新时间。自动缓存可失效重建；人工确认不得被随后一次低置信自动解析覆盖。字典变更使自动缓存失效；人工映射保留但显示其确认版本，需复核时不自动改业务数据。

parse 可更新缓存命中信息；confirm 只写缓存；cache 查询只读。任何一次调用都不隐式新建生产批、归组单据或更正历史。单据写入是否采用某个解析值由调用方显式决定。

## 5. 输入与错误

未知字段、类型错误、非法 JSON、缺控制参数作为协议错误；负数、非法日期、无效引用、非“片”计量单位及抽检 LOSS 不能通过“只警告”绕过。资料未填齐、参考不匹配、多类别重复计数和数量核对差异以 issues 返回。

所有业务计数区分 null 与 0，不使用零值结构体把缺失转成 0。日期值与时点值互斥，不用系统录入时间补业务日期。数组替换和对象合并只依 API 明确规定，不推测行级合并。

表头 counts 是三项正式数量和良率的唯一来源。明细计数只做合计核对，不回填表头；纸面数量声明及其独立原始值不进入字段目录、内容快照、存储投影或接口。历史版本仍保留每次实际保存的表头和明细，样品原文与数量变化说明继续按各自规则保存。

错误包含稳定 code、可读中文 message、可编程 details.reason、必要的字段 path。日志关联用 X-Request-Id，幂等用 X-Idempotency-Key，两者不能互代。500 不暴露 SQL、堆栈和凭据。失败事务不得产生部分版本、占用或成功缓存。

## 6. 留痕与证据

每次内容保存追加完整快照，记录系统操作者、系统时间、可选修改原因及前一版本 ID。取消、归组和填报状态变化记录 before/after。账户和字典维护、登录成功/失败及会话撤销记录最小必要审计，密码和完整 token 永不写入日志或审计。

业务版本用于查内容，审计用于查动作和回放对象状态；解析缓存不能替代二者。业务历史与审计不提供普通修改/删除 API。应用层追加写不等于数据库管理员无法篡改；证据保护依赖数据库权限与独立备份，不能作超出部署能力的承诺。

人员文字是检验内容；系统操作人是可信身份。IP 和 User-Agent 只用于排障，不作为授权依据。只采集必要的人员资料，日志避免记录整份请求正文。

## 7. 部署与恢复约束

支持本机 SQLite 单实例和 API＋PostgreSQL 服务器部署。生产使用 HTTPS，明确可信代理、外部来源和凭据策略。Cookie 使用 HttpOnly、SameSite=Lax、Path=/，HTTPS 开启 Secure。Cookie 写请求校验 Origin 属于允许来源，不能以 CORS 代替写入来源校验；跨站场景优先使用明确约定的 Bearer 调用。

CORS 只允许配置的来源；携带凭据不能使用通配来源。允许 Authorization、Content-Type、X-Idempotency-Key、X-Request-Id，暴露 X-Request-Id、X-Idempotent-Replay。请求体、执行时间和数据库锁等待须有上限，不能无限等待。

数据库/配置/备份外置。启动时连接或必要结构检查失败则明确失败；退出停止接收新请求并有界等待在途事务。只有数据库事务提交后才返回成功。

每日一致备份、保留 30 天为运维设计基线。SQLite 使用 backup API 或停写后一致备份，不在 WAL 写入期间仅复制主文件；PostgreSQL 使用 pg_dump，按实际恢复目标补充 WAL 策略。恢复演练检查账户、字典、单据、内容版本、审计、外键和报告历史起点。

质量记录保留期、正式部署位置及 RPO/RTO 由业务与运维确认；未确认前不自动清理记录。结构变更按序迁移，已发布迁移不改写；禁止在服务启动时清空业务数据。本文不授权执行清库或删除历史数据。

## 8. 性能与验证

设计规模为数十人并发、每年 10^4–10^5 张单。目标为单笔保存 P95 <500ms、千笔条件查询 P95 <2s；须在标明数据库、硬件、并发数、版本数与数据分布的环境验证。历史回放、缺陷汇总及全范围报告单独测量，不能以普通详情响应耗时代替。

索引覆盖生产批身份、工序组、范围占用、版本归属、工序/业务日、样品维度、机台和人员。汇总先固定单据版本集合，再分别聚合计数、缺陷与变化条目，避免多表 JOIN 乘倍。分组结果支持分页，不静默截断。

必要验证覆盖：补丁三态、数组替换、未知与零、辅助解析不改原文、缓存确认不改历史、同类账户更正、跨类型拒绝、幂等回放、并发防重、保存/取消与统计一致、同秒历史视图、两库事务回滚及备份恢复。本文不记录工程完成率或测试通过状态。

## 9. 共享存储结构

下列为目标逻辑结构，具体 SQL 方言分别适配两库。所有外键都以稳定 ID 关联，不从显示文字重建关系；历史快照中的文字是当时证据，不与目录标签级联更新。

| 表 | 必要列 | 唯一性/索引/约束 |
|---|---|---|
| users | id,username,display_name,employee_no,department,password_hash,is_admin,account_type,status,failed_attempts,locked_until,last_login_at,created_at,updated_at | username唯一；非空employee_no唯一；account_type/status枚举；历史引用不级联删除 |
| sessions | id,user_id,token_hash,created_at,last_seen_at,expires_at,revoked_at,ip,user_agent | token_hash唯一；user_id+revoked_at索引；用户FK；仅令牌哈希落库 |
| dict_items | id,category,code,label,sort_no,active,meta_json,created_at,updated_at | category+code唯一；category+active+sort_no索引；meta为合法JSON对象/null |
| measurement_units | id,code,name | 固定PIECE/片，应用只读；不存部门或组织 |
| sample_drawings | id,seq_no,drawing_no,project_code,model_code,spec_code,size_code,side_code,class_code,is_rx,active,note,created_at,updated_at | drawing_no唯一；非空seq_no唯一；维度复合索引；is_rx可空 |
| sample_parse_cache | id,normalized_key,raw_sample,parsed_json,verified,verified_by,verified_at,dictionary_version,parser_version,created_at,updated_at | normalized_key唯一；人工确认人可空；辅助缓存可重建，不能反向更新检验版本 |
| field_schemas | schema_version,definition_json | schema_version唯一；包括全部字段、类型、可空性、105格缺陷顺序和只读派生路径；发布后版本不改写 |
| form_templates | template_version,operation_id,schema_version,layout_json | template_version唯一；引用不可变schema；通用模板operation_id可空；旧模板保留 |
| idempotency_results | user_id,idempotency_key,account_type,request_fingerprint,http_status,response_json,created_at,expires_at | user_id+key唯一；仅成功事务生成；expires_at索引；不能把业务历史随TTL删除 |
| audit_logs | event_id,transaction_id,actor_id,actor_name,action,entity,entity_id,before_json,after_json,reason,created_at,ip,user_agent | event_id递增；实体+ID+event_id、actor_id+created_at索引；不提供普通更新/删除 |
| ledger_history_meta | singleton_id,history_started_at,last_event_time | 单行；历史起点与最近逻辑业务时点，不绑定某个迁移编号 |
| ledger_write_lock | singleton_id,lock_version | 单行数据库写闸门，领域事务首先取得锁 |

业务模型表的必要列和占用约束见[系统设计](design.md#2-数据模型与约束)。内容快照和查询投影同事务生成，快照是可验证证据，投影是查询加速，二者不能分别对外提供写入接口。

业务动作的审计行使用同一事务 ID 分组，报告上界只接受完整领域事务的末事件。共享维护/认证日志可以出现在同一全局日志序列中，但不能仅凭其事件号宣称已固定一个检验业务快照。数据库自增编号存在间隙合法，不以连续无间隙作为历史完整性的证明。
