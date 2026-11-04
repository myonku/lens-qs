# v2 正式系统设计

版本 2.0.0，2026-11-04。依据 [需求](requirements.md) 与 [接口](api.md)。

## 1. 架构与领域

Go + Gin + sqlx 单体，SQLite/PostgreSQL 两套手写迁移。账户、会话、字典、参考清单、样品解析为共享基础能力；ledger 为唯一检验业务域。不再装配 inspection/stats/idempotency 旧包。所有接口统一 /api/v2，healthz 保持独立，根信息报告 2.0.0。

product_lots 共享身份唯一 (shared_form_no,sample_identity_key)，只 trim；operation_groups 唯一 (product_lot_id,operation_id,inspection_kind)，维护 OPEN/CLOSED 与 EMPTY/WHOLE/SPLIT。inspection_submissions 稳定 ID、version、当前正式/草稿指针；submission_slots 的组+规范批号主键防重，整批内部键空字符串。组合外键校验组、类型及单据/版本归属。

submission_revisions 保存完整 TEXT JSON 内容快照，并有正式计数、规范日期/时点/业务日与常用查询列；revision_lines 保留105格，quantity_changes 独立表。保存追加版本，不覆盖历史；发布元信息一次性设置。counts/declared_counts/明细不互相静默覆盖。数量变化不自动扣改计数。

## 2. 账户填报隔离

users.account_type TEXT NOT NULL DEFAULT NORMAL，CHECK NORMAL/SAMPLING。is_admin 是独立的账户管理能力。Principal与用户列表/创建/修改结构携带account_type；登录及me额外返回派生的submission_inspection_kind。维护用户类型时同事务撤销 sessions 并删除用户 v2 幂等缓存，密码哈希保持。

ledger 写事务获取数据库写闸门后读取当前 users.status/account_type；幂等指纹还绑定该类型。创建工序组和未归组草稿时缺省采用账户类型，显式相反类型 403。引用已有组/单的所有变更先验证类型（包括绑定、关组/开放），随后执行版本/状态/slot校验。管理员写业务也适用。读取目前不按类型限制，前端负责默认显示/固定填报标记。

## 3. 事务和并发

检验业务写入均强制 X-Idempotency-Key，修改已有对象强制 expected_version。同键同目标/原文/账户类型回放，回放先于旧版本判断。数据库全局 v2_write_lock 闸门在业务读之前串行化写入，SQLite默认temp_store=MEMORY避免Windows临时文件失败（自定义URI也补齐连接配置）；首条 UPDATE 获取写锁，PostgreSQL 同行锁；等待及事务有界。slot、版本、审计与响应结果同事务，任一失败全部回滚，不以存储故障放行业务。

绑定已归组身份不可改；取消释放slot，组模式按剩余占用维护；组已关闭不能新建或绑定，已有正式单仍可修订/取消。批量内部冲突和越权均整体回滚。

## 4. 历史时点报告

使用已有 audit_logs 的自增 id 作为事件序号，created_at 为 UTC 秒。按 created_at<=as_of、id升序回放 product_lot/operation_group/inspection_submission 的 after 快照，在一个一致读事务内形成截止时点对象状态；仅纳入已存在的组和单。组的slot模式、version由占用变更时补记的组审计恢复，不从今天的状态倒推。0006为已有v2对象写入统一切换时点的基线，之前时间返回history_unavailable。

单据的正式/草稿指针来自当时审计快照，内容从不可覆盖版本读取；后来设置的发布元信息在早期快照中屏蔽，避免未来信息泄露。单据取消后的当前指针只供历史，不能计正式结果。组新增/slot占用释放均审计，防止历史组版本停留在创建版本。返回截止事件 id、实际纳入版本集合及 as_of；截止秒内全部事件按id排序纳入，不承诺亚秒时间查询。

默认当前快照沿用同一报告结构。可见业务工序为该产品批已建立的填报组；未建组不伪造“未生产”。产品批跨工序不提供产品总量。

## 5. 工序范围报告与统计

GET reports/operations/{operation_id} 要求 from/to，可选 product_lot_id、inspection_kind、date_basis、分页。资料取当前正式版，未发布取当前草稿；可列取消单但正式小计只取当前 PUBLISHED。修订草稿另列，不双计。分页只影响 items，全量小计/分产品批分区/版本集合基于整个筛选范围。

默认 date_basis=inspection_day：from/to 日期包含首尾，paper_inspected_date 直接归日，准确时点按 Asia/Shanghai 归日；未知日数量单列。created_at 使用 UTC [from,to)。报告响应明确为当前版本在所选业务/创建时间范围内的查询，不假称历史时点快照；历史回放使用产品批 as_of 接口。

正式统计先选版本集合，再分别聚合计数/缺陷/变化，避免明细连接乘倍。未知项返回 known_sum/unknown_count/complete/value，非完整value=null。良率在数量完整且0<分母、分子<=分母时计算。ALL 的摘要/缺陷/变化均分 NORMAL/SAMPLING，抽检 LOSS 不入损耗聚合。

## 6. 日期与正式切换

业务日期/时点/系统时间物理列均TEXT，标准日期YYYY-MM-DD，时点UTC秒精度；原始纸面文字独立。日期与时点二选一，切换精度PATCH需清空旧字段。非法日期、无时区/小数秒时点拒绝。

0006 新增账户类型并删除旧业务表、旧幂等及迁移候选表，旧表清理不可逆；down迁移不能假装恢复被删除业务证据。保留0001–0005迁移文件仅供升级链，运行代码不再查询旧表。已有v2业务不由0006自动清空；测试业务通过显式reset-testdata命令清理。命令保留users/字典，清业务审计/幂等/会话/解析缓存并设置报告历史起点，防止清空后回放已删除数据。

真实环境部署先停旧调用方写入、备份并确认测试数据范围，再迁移并更换全部调用方路径。历史施工记录只描述当时结果，当前设计以本文件为准，阶段状态统一见[项目进度](progress.md)。
