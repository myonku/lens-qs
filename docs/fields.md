# v2 字段目录

本文定义纸面栏目、样品信息和检验内容的字段含义。接口封装见 [API](api.md)，身份与版本关系见 [系统设计](design.md)。栏目数量不等于 JSON 字段数量；计算值、身份字段与可编辑内容分别列明。

## 1. 身份与填报标记

| 字段 | 中文 | 含义 |
|---|---|---|
| shared_form_no | 共享表单号 | 生产批在各工序共用的业务编号，不是单张检验单 ID |
| sample_identity | 完整样品型号 | 用于识别生产批的完整业务文字，不等于解析所得的型号片段 |
| product_lot_id | 生产批 ID | 系统稳定关联键 |
| operation_id | 工序 ID | 工序字典项稳定 ID；工序显示名称单独返回 |
| inspection_kind | 检验类型 | NORMAL 正常检验 / SAMPLING 抽检，与整批或分批无关 |
| batch_no | 检验批号 | 当前工序内的分批标识；空值代表当前工序的整批范围 |
| machine_no | 机台号 | 本检验批次使用的设备文字；同一设备可对应多张单据 |

工序、批号、机台是不同概念。明细行仅是单据内部的计数行，不是更小的检验批次。相邻工序的同名批号不建立实物对应关系。

## 2. 纸面栏目

| 纸面栏目 | 内容字段 | 类型 | 说明 |
|---|---|---|---|
| 单位 | paper_unit_id / paper_unit_text | string/null | 计量单位固定为“片”，可传固定目录 ID 或文字“片”；其他值后端拒绝，不得用用户部门代填 |
| 送检人 | paper_submitter | string/null | 纸面人名文字 |
| 送检时间 | paper_submitted_date / paper_submitted_at | date/instant/null | 同一事件两种精度，至多一项非空 |
| 检验人 | paper_inspector | string/null | 纸面实际检验人员，不是代录账号 |
| 检验时间 | paper_inspected_date / paper_inspected_at | date/instant/null | 与系统录入时间分开 |
| 操机员 | paper_machine_operator | string/null | 纸面人名文字 |
| 交接员 | paper_handover_by | string/null | 纸面人名文字；记录此人不代表存在收发交接流水 |

人员不要求对应系统账号。计量单位可未知，缺项只提示；单位有值时必须合法，不能以“不完整”为由保存其他单位。用户的部门资料与纸面计量单位分开。日期未知为 null；只知日期不补造零点。时间传输与校验详见 API 的日期约定。

## 3. 样品信息

| 字段 | 含义 | 示例 |
|---|---|---|
| sample_raw | 本张检验单的样品原文，逐字保存 | M2 S2 ND2 SMR |
| sample_project | 项目 | M1、M2 |
| sample_model | 型号片段 | VID1、VID1 Rx、VID2 |
| sample_spec | 规格 | S1、S2 |
| sample_size | 大小 | MD、SM、LG |
| sample_side | 左右 | L、R |

五个结构化字段均须可录入、检索、分组，缺失允许保存。项目不能从参考部件号推定。层级“项目→型号→规格→大小→左右”不规定原文的书写位置。

解析/人工确认只改辅助结构化结果，不自动改写原文；若原文本身录错，应显式修改并生成新内容版本。修改本单的样品字段不改变生产批身份。原文与结构化字段不一致时显示两者，由人核对，不反向拼接覆盖原文。

结构类别 class、处方标记 is_rx、参考图号 drawing_no、参考序号 seq_no、unknown_tokens、confidence 是解析辅助结果。MONOC/BINOC/Rx 的现场含义未定，不作为必填或保存门槛；缺少明确标记时保持未知，不能从未出现 Rx 自动证明 is_rx=false。

## 4. 计数、明细及备注

| 字段 | 含义 | 约束 |
|---|---|---|
| counts.total_qty | 表头检验总数，正式统计来源 | 非负整数/null |
| counts.qualified_qty | 表头合格品数，正式统计来源 | 非负整数/null |
| counts.defect_qty | 表头不合格品数，正式统计来源 | 非负整数/null |
| pass_rate | 良率，百分数 | 服务端计算，只读；无有效分母时为 null |
| lines | 单据计数/缺陷明细 | 数组，可空数组；不代表设备或批次执行分段 |
| lines[].seq | 明细行序号 | 正整数，同一内容版本内唯一 |
| lines[].total_qty / qualified_qty / defect_qty | 行计数，仅合计核对表头 | 非负整数/null |
| lines[].defects | 类别与表面的计数对象 | 每格非负整数/null，详见下表 |
| lines[].remark / remark | 行备注 / 单据备注 | string/null |

空白与零不同：null 或未填表示不知道，0 表示明确没有。表头 counts 是三项正式数量及良率的唯一来源；明细计数不回填或覆盖表头，表头未知不会由明细补齐。不提供 declared_counts 或纸面数量声明的独立原始值字段。line_totals 和 count_differences 为只读核对值，结构见 API。

缺陷格每格记录“具有该类缺陷的镜片数量”，不是比例或缺陷点个数。同片可能跨类别重复计入，缺陷格子合计不能作为唯一不合格品数。表格下方额外手写率值（例如 A：100%）不建字段。

## 5. 缺陷编码与顺序

每个类别固定三个表面，顺序为 `concave`（凹面）、`convex`（凸面）、`both`（双面）。JSON 路径示例：`lines[0].defects.prick.concave`。未填写的格子为未知，禁止在保存或统计时自动补零。

| 顺序 | 编码 | 中文 |
|---|---|---|
| 1 | prick | 点伤 |
| 2 | scratch | 划伤 |
| 3 | flange_prick | 法兰边点伤 |
| 4 | flange_scratch | 法兰边划伤 |
| 5 | collapse_edge | 崩边 |
| 6 | coating_particle | 涂层瑕疵 |
| 7 | bright_spot | 亮点 |
| 8 | crack | 裂 |
| 9 | overflow_glue | 溢胶 |
| 10 | discolor_particle | 变色不良颗粒 |
| 11 | protective_particle | 保护不良颗粒 |
| 12 | spin_particle | 旋涂颗粒 |
| 13 | fiber | 纤维 |
| 14 | flange_particle | 法兰边颗粒 |
| 15 | hardening_bubble | 加硬气泡 |
| 16 | hairline_crack | 发丝裂 |
| 17 | dirt | 脏污 |
| 18 | spin_mark | 旋痕 |
| 19 | raised_point | 凸点 |
| 20 | recess | 凹坑 |
| 21 | bubble | 气泡 |
| 22 | shape | 面形 |
| 23 | watermark | 水印 |
| 24 | hard_spot | 加硬点 |
| 25 | flange_round_point | 法兰边圆点 |
| 26 | severe_flange_damage | 严重法兰边伤 |
| 27 | garbage | 垃圾 |
| 28 | flange_damage | 法兰边伤 |
| 29 | flange_imprint | 法兰边印痕 |
| 30 | press_imprint | 压痕/法兰边压痕 |
| 31 | flow_mark | 流痕 |
| 32 | inner_fold | 镜内褶皱 |
| 33 | flange_fold | 褶皱/法兰边褶皱 |
| 34 | inner_flow_glue | 内挂流/挂流 |
| 35 | flange_recess | 法兰边凹痕 |

35 类 × 3 表面共 105 格。“加硬点”与“垃圾”的现场判定标准由业务说明，系统保留两个独立类别。双面与单面是否重叠不作去重猜测。

## 6. 数量变化说明

| 字段 | 类型 | 含义 |
|---|---|---|
| kind | LOSS / ADJUSTMENT / OTHER | 损耗 / 数量调整 / 其他说明 |
| direction | DECREASE / INCREASE / UNKNOWN | 减少 / 增加 / 方向未知；LOSS 仅 DECREASE |
| quantity | 非负整数/null | 可只写原因，数量未知 |
| reason | 非空 string | 本条说明内容 |
| occurred_date / occurred_at | date/instant/null | 可未知；至多一个非空 |

条目从属于某一内容版本。修订时完整替换数组，历史条目保留。损耗不是不合格品的别名；保存损耗不自动扣 counts。SAMPLING 抽检禁止 LOSS，前后端都校验；封存、返还等不能一概当成 LOSS。方向未知的变化单列，不强行正负相抵；不同单据同一原因也不证明是互不重复的实物事件。

## 7. 系统留痕与派生字段

系统生成单据 ID、内容版本 ID/号、对象 version、创建人/时间、最近保存人/时间、取消人/时间/原因。每次修改有可信系统操作人及变更前后记录。调用方不得写入或伪造这些值。

`is_complete/missing_fields` 只描述资料缺项；`issues` 描述核对提示；`pass_rate` 描述可计算的检验结果。这三者不互相替代，不表达审批通过、实物流转完成或全流程质量结论。
