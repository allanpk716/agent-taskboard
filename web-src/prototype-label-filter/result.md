# 动态标签筛选原型修改结果

## 1. 改动摘要

- 将工单数据调整为 30 张（28 张进行中、2 张已关闭）：机型计数为 UD-2300 10、UD-1600 7、BH-7600CS 4、UD-3100 3、BH-5800 2、UD-900 1；系统计数为液路 8、软件 6、打印 4、质控 3、光路 2、条码 1；科室、产品线、其他标签也呈不均分布。
- 关键组合「机型 UD-2300 或 UD-1600，且系统液路」为 6 张；再排除「待初筛」后为 3 张。多选后科室和其他标签计数明显收缩，体检中心、病理科等值可变为 0 并置灰。
- A 分面面板、A 手机抽屉、B 按维度浏览的值列表在分组上下文内只显示裸值；C 下拉值列表保持裸值。列表行、信息栏、B 输入建议和已选条件 chip 仍保留带维度写法。
- 同步更新 `verify.mjs` 的独立数据副本，并新增多选数量 5-7 的断言。

## 2. 验收命令

### `sh build.sh a b c`

退出码：`0`

输出末尾：

```text
（命令无输出）
```

### `node verify.mjs`

退出码：`0`

输出末尾（30 行）：

```text
PASS b/1366 row chip adds condition | label=机型:UD-2300
PASS c/1366 multi count | page=6, expected=6
PASS c/1366 exclude count | page=3, expected=3
PASS c/1366 exclude narrows | exclude=3, multi=6
PASS c/1366 row chip adds condition | label=机型:UD-2300
PASS a/1024 multi count | page=6, expected=6
PASS a/1024 exclude count | page=3, expected=3
PASS a/1024 exclude narrows | exclude=3, multi=6
PASS a/1024 no horizontal overflow | {"docs":true,"app":true,"listOk":true,"dw":1024,"dc":1024,"aw":1024,"ac":1024}
PASS b/1024 multi count | page=6, expected=6
PASS b/1024 exclude count | page=3, expected=3
PASS b/1024 exclude narrows | exclude=3, multi=6
PASS b/1024 no horizontal overflow | {"docs":true,"app":true,"listOk":true,"dw":1024,"dc":1024,"aw":1024,"ac":1024}
PASS c/1024 multi count | page=6, expected=6
PASS c/1024 exclude count | page=3, expected=3
PASS c/1024 exclude narrows | exclude=3, multi=6
PASS c/1024 no horizontal overflow | {"docs":true,"app":true,"listOk":true,"dw":1024,"dc":1024,"aw":1024,"ac":1024}
PASS a/390 multi count | page=6, expected=6
PASS a/390 exclude count | page=3, expected=3
PASS a/390 exclude narrows | exclude=3, multi=6
PASS a/390 no horizontal overflow | {"docs":true,"app":true,"listOk":true,"dw":390,"dc":390,"aw":390,"ac":390}
PASS b/390 multi count | page=6, expected=6
PASS b/390 exclude count | page=3, expected=3
PASS b/390 exclude narrows | exclude=3, multi=6
PASS b/390 no horizontal overflow | {"docs":true,"app":true,"listOk":true,"dw":390,"dc":390,"aw":390,"ac":390}
PASS c/390 multi count | page=6, expected=6
PASS c/390 exclude count | page=3, expected=3
PASS c/390 exclude narrows | exclude=3, multi=6
PASS c/390 no horizontal overflow | {"docs":true,"app":true,"listOk":true,"dw":390,"dc":390,"aw":390,"ac":390}
PASS no JS or console errors
```

## 3. 截图描述

- `shots/a-open-1366.png`：A 分面面板按维度分组，机型、系统等计数呈 10/7/4/3/2/1 和 8/6/4/3/2/1 的阶梯分布，组内值如 `UD-2300`、`液路` 不带维度前缀。
- `shots/c-open-1024.png`：C 展开机型下拉，六个裸值按 10、7、4、3、2、1 显示联动计数，顶部列表行和已选 chip 仍保留完整标签语境。

## 4. 偏离之处

无。

## 5. 范围外发现

无。
