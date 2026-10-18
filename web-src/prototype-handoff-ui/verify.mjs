import { chromium } from 'file:///C:/Users/allan716/AppData/Roaming/npm/node_modules/@playwright/cli/node_modules/playwright/index.mjs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = path.dirname(fileURLToPath(import.meta.url))
const candidates = ['a', 'b', 'c']
const scenes = [
  ['s1', 'PC 工单详情'],
  ['s2', '改派完成'],
  ['s3', '手机工单详情'],
  ['s4', '我的 → Agent'],
]
const identities = [
  ['wang', '我是王梦龙（指派人）'],
  ['chen', '我是陈工（发起人）'],
]
const viewports = [[1366, 900], [390, 844]]
const humanLine = '王梦龙：陈工派给你'
const errors = []
const assertions = []
let shots = 0

function check(name, ok, detail = '') {
  assertions.push(`${ok ? 'PASS' : 'FAIL'} ${name}${detail ? ` | ${detail}` : ''}`)
  if (!ok) process.exitCode = 1
}

const browser = await chromium.launch({
  executablePath: 'C:/Users/allan716/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe',
})

for (const [width, height] of viewports) {
  const context = await browser.newContext({ viewport: { width, height } })
  for (const candidate of candidates) {
    const page = await context.newPage()
    page.on('console', msg => { if (msg.type() === 'error') errors.push(`${candidate} ${width}: ${msg.text()}`) })
    page.on('pageerror', err => errors.push(`${candidate} ${width}: ${err.message}`))
    const url = 'file:///' + path.join(root, `${candidate}.html`).replaceAll('\\', '/')

    for (const [who, whoLabel] of identities) {
      await page.goto(url)
      await page.getByRole('button', { name: whoLabel }).click()
      for (const [scene, sceneLabel] of scenes) {
        await page.getByRole('button', { name: sceneLabel }).click()
        await page.waitForTimeout(120)
        const shot = path.join(root, 'shots', `${candidate}-${scene}-${who}-${width}.png`)
        await page.screenshot({ path: shot })
        shots++

        const visibleText = await page.locator('body').innerText()
        if (who === 'wang' && (scene === 's1' || scene === 's3')) {
          check(`${candidate} ${scene} wang hides human line`, !visibleText.includes(humanLine))
        }
        if (who === 'chen' && scene === 's2') {
          if (candidate === 'c') {
            await page.locator('.timeline-card button').click()
            await page.locator('.toast:has-text("已复制交接文案") button').waitFor({ timeout: 5000 })
            await page.locator('.toast:has-text("已复制交接文案") button').click()
          }
          const sceneText = await page.locator('body').innerText()
          const areaTexts = await page.locator('textarea').allInnerTexts()
          const values = await Promise.all((await page.locator('textarea').all()).map(x => x.inputValue()))
          check(`${candidate} s2 chen shows human line`, [sceneText, ...areaTexts, ...values].some(x => x.includes(humanLine)))
        }
      }
    }

    if (width === 1366) {
      await page.goto(url)
      const thread = await page.locator('.thread').boundingBox()
      const composer = await page.locator('.composer').boundingBox()
      const meta = await page.locator('.meta').boundingBox()
      check(`${candidate} PC conversation fills middle`, thread.width > 400 && Math.abs(thread.width - composer.width) < 2, `thread=${thread.width}, composer=${composer.width}, meta=${meta.width}`)

      if (candidate === 'c') {
        const card = await page.locator('.timeline-card').boundingBox()
        const text = await page.locator('.timeline-card .txt').boundingBox()
        const button = await page.locator('.timeline-card button').boundingBox()
        check('c assignment card is horizontal', button.x > text.x + text.width && card.width > 250, `card=${card.width}, text=${text.width}`)
        await page.locator('.timeline-card button').click()
        await page.locator('.toast button', { hasText: '查看内容' }).click()
        const modal = await page.locator('.modal').boundingBox()
        const area = await page.locator('.handoff-textarea').boundingBox()
        check('c copied-text textarea fills modal', area.width > modal.width * 0.9, `area=${area.width}, modal=${modal.width}`)
      }

      if (candidate === 'b') {
        const copyButtons = await page.getByRole('button', { name: '复制交接文案' }).count()
        check('b modal has one main copy button', copyButtons === 1, `count=${copyButtons}`)
      }

      await page.getByRole('button', { name: '我的 → Agent' }).click()
      await page.getByRole('button', { name: '复制', exact: true }).click()
      check(`${candidate} token toast`, (await page.locator('.toast').innerText()).includes('已复制 token'))
      await page.waitForTimeout(2700)
      check(`${candidate} toast auto-dismisses`, await page.locator('.toast').count() === 0)
    }
    await page.close()
  }
  await context.close()
}

await browser.close()
console.log(`screenshots=${shots}`)
console.log(`console_errors=${errors.length}`)
for (const error of errors) console.log(error)
for (const assertion of assertions) console.log(assertion)
