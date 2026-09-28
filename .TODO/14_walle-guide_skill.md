# walle-guide Skill

## 目标

制作一个给用户使用的 `walle-guide` skill：当用户想让 AI 修改 walle 配置时，引导 AI 先理解配置文件位置、字段含义和安全边界，再做最小修改。

## 预期位置

- `.walle/skills/walle-guide/SKILL.md`

## 需要覆盖

- walle 的用户配置目录：`~/.walle/`
- 常见配置类型：模型、provider、prompt、skill
- 修改配置前先读取现有文件，不凭空覆盖
- 只改用户明确要求的配置项
- 修改后说明改了哪个文件、哪个配置项

## 待办

- [ ] 确认当前配置文件格式和路径。
- [ ] 编写 `.walle/skills/walle-guide/SKILL.md`。
- [ ] 用一个简单场景验收：让 AI 修改默认模型配置。

## 边界

- 不做开发者贡献指南。
- 不覆盖代码架构导航。
- 不新增运行时代码。
