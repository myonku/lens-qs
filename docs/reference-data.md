# v2 样品参考资料

本文完整列出供样品辅助解析和选择使用的参考资料。该目录不是产品准入清单，不决定生产批身份；清单外的样品原文照常保存。其字段含义见 [字段目录](fields.md)。

## 1. 基础维度

| 类别 | 初始候选值 | 用途 |
|---|---|---|
| inspection_operation | 一次加硬、一次固化、一次清洗、PhCh旋涂变色、紫外线保护、二次加硬、二次固化、ARC镀膜、DMC打码、CNC裁型、Logo打标、二次清洗、OQC+包装 | 工序目录，可维护，不限定真实生产路线 |
| sample_project | M1、M2 | 项目，独立录入，不能从下面的图号清单反推 |
| sample_model | VID1、VID1 Rx、VID2 | 型号片段 |
| sample_spec | S1、S2 | 规格 |
| sample_size | MD、SM、LG | 大小 |
| sample_side | L、R | 左右 |
| sample_class | MONOC、BINOC、MONOC_AND_BINOC | 可选参考属性，最后一项显示为 MONOC & BINOC |

以上为候选值，不是样品内容白名单。字典别名可辅助匹配；没有明确别名时不把 ND2 猜成 VID2。完整身份型号是另一独立字段，不通过拼接这些维度自动生成。

工序清单以 [`backend/data/inspection_operations.csv`](../backend/data/inspection_operations.csv) 为准，由后端启动时同步；维护规则见[后端说明](../backend/README.md#工序映射表与启动同步)。旧库 GENERAL 保留兼容，不在新清单中自动新增。

## 2. 参考部件号

| 序号 | 部件号原文 | 型号 | 规格 | 大小 | 左右 | 结构类别标记 |
|---|---|---|---|---|---|---|
| 05 | S1 - MD - R - VID2 - MONOC & BINOC | VID2 | S1 | MD | R | MONOC & BINOC |
| 06 | S1 - MD - L - VID2 - MONOC & BINOC | VID2 | S1 | MD | L | MONOC & BINOC |
| 07 | S1 - LG - R - VID2 - MONOC & BINOC | VID2 | S1 | LG | R | MONOC & BINOC |
| 08 | S1 - LG - L - VID2 - MONOC & BINOC | VID2 | S1 | LG | L | MONOC & BINOC |
| 09 | S2 - SM - R - VID2 - MONOC | VID2 | S2 | SM | R | MONOC |
| 10 | S2 - SM - L - VID2 | VID2 | S2 | SM | L | 未标注 |
| 11 | S2 - MD - R - VID2 - MONOC | VID2 | S2 | MD | R | MONOC |
| 12 | S2 - MD - L - VID2 - MONOC | VID2 | S2 | MD | L | MONOC |
| 13 | S1 - MD - L - VID1 Rx | VID1 Rx | S1 | MD | L | 未标注 |
| 14 | S2 - SM - L - VID1 RX | VID1 RX | S2 | SM | L | 未标注 |
| 15 | S1 - MD - R - VID1 Rx MONOC & BINOC | VID1 Rx | S1 | MD | R | MONOC & BINOC |
| 16 | S1 - MD - L - VID1 Rx MONOC | VID1 Rx | S1 | MD | L | MONOC |
| 17 | S1 - LG - R - VID1 Rx MONOC & BINOC | VID1 Rx | S1 | LG | R | MONOC & BINOC |
| 18 | S1 - LG - L - VID1 Rx BINOC | VID1 Rx | S1 | LG | L | BINOC |
| 19 | S1 - LG - L - VID1 Rx MONOC | VID1 Rx | S1 | LG | L | MONOC |
| 20 | S2 - SM - R - VID1 RX | VID1 RX | S2 | SM | R | 未标注 |
| 21 | S2 - MD - R - VID1 RX | VID1 RX | S2 | MD | R | 未标注 |
| 22 | S2 - MD - L - VID1 RX | VID1 RX | S2 | MD | L | 未标注 |

原文 Rx/RX 大小写保留。结构类别未标注即未知，不补齐；Rx 的解释不参与检验数据校验。参考资料可以维护，变更只影响后续辅助解析，不批量回填历史检验版本。

## 3. 解析约束与示例

1. 原文单独保留。全半角、大小写、空白和分隔符归一化仅用于匹配及缓存键。
2. 按 token 集合匹配维度，不按字符串位置取字段。组合标记保留组合语义。
3. 对 `M2 S2 ND2 SMR`，可识别 M2/S2；ND2 没有别名时进入 unknown_tokens。SMR 只有在明确的组合拆解规则命中时才拆成 SM/R，不能把任意字符串按末尾字母拆开。
4. 对 `S1 - MD - R - VID2 - MONOC & BINOC`，可匹配参考项 05，但项目仍为未知；不能因匹配到图号补出 M1 或 M2。
5. 同一维度出现互相冲突的候选值时返回候选和提示，等待人工选定，不按任意遍历顺序选一个。
6. 人工确认映射记录确认人和时间，不自动创建生产批或修改检验单。缓存失效、字典更新和解析器升级均不得改写业务历史。
