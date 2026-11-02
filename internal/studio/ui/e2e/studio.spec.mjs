import { test, expect } from '@playwright/test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

let project;
let promptFile;

test.beforeEach(async () => {
  project = await fs.mkdtemp(path.join(process.env.HOME || os.homedir(), 'am-studio-e2e-'));
  const scene = path.join(project, 'scenes', 'scene-001');
  await fs.mkdir(scene, { recursive: true });
  await fs.writeFile(path.join(project, 'article-to-motion.conf'), 'ORCHESTRATOR=claude\nRENDERER=claude\n');
  await fs.writeFile(path.join(project, 'PROMPT.md'), '整片制作提示词\n');
  await fs.writeFile(path.join(scene, 'voice.srt'), '1\n00:00:00,000 --> 00:00:01,000\nhello\n');
  await fs.writeFile(path.join(scene, 'prompt.md'), 'before\n');
  await fs.writeFile(path.join(scene, 'scene.json'), JSON.stringify({
    id: 'scene-001', duration_seconds: 1, output: 'out.mp4', transcript: 'voice.srt', text: 'hello'
  }));
  promptFile = path.join(scene, 'prompt.md');
});

test.afterEach(async () => { await fs.rm(project, { recursive: true, force: true }); });

async function openProject(page) {
  await page.goto('/');
  await page.locator('#open-project-trigger').click();
  await page.locator('#mode-open').click();
  await page.locator('.directory-row.project-row', { hasText: path.basename(project) }).click();
  await page.locator('#open-project').click();
  await expect(page.locator('#title')).toHaveText(path.basename(project));
  await expect(page.locator('.scene', { hasText: 'scene-001' })).toBeVisible();
}

test('asks for a change, reviews the diff, and reverts the turn', async ({ page }) => {
  await openProject(page);
  await page.locator('#prompt').fill('把镜头提示词改成 after');
  await page.locator('#send').click();
  await expect(page.locator('#messages')).toContainText('已把镜头提示词改好');
  await expect(page.locator('.tool', { hasText: 'Write' })).toBeVisible();
  const card = page.locator('.turn-card');
  await expect(card).toContainText('scenes/scene-001/prompt.md');
  await expect.poll(() => fs.readFile(promptFile, 'utf8')).toBe('after\n');

  await card.locator('button', { hasText: 'scenes/scene-001/prompt.md' }).click();
  await expect(page.locator('#diff .del')).toContainText('before');
  await expect(page.locator('#diff .add')).toContainText('after');
  await page.locator('[aria-label="关闭"]').last().click();

  await page.locator('#revert-turn').click();
  await expect(card).toContainText('已回退这一轮');
  await expect.poll(() => fs.readFile(promptFile, 'utf8')).toBe('before\n');

  // 刷新后会话从项目里的事件记录恢复。
  await page.reload();
  await expect(page.locator('#messages')).toContainText('已把镜头提示词改好');
  await expect(page.locator('.turn-card')).toContainText('已回退这一轮');
});

test('starts production and can stop a running turn', async ({ page }) => {
  await openProject(page);
  await page.locator('#start-production').click();
  await expect(page.locator('#messages')).toContainText('开始整片制作');
  await expect(page.locator('.turn-card')).toContainText('本轮完成');
  await expect(page.locator('#session-select option:checked')).toContainText('整片制作');

  await page.locator('#prompt').fill('慢慢来，先别动');
  await page.locator('#send').click();
  await expect(page.locator('#cancel-turn')).toBeVisible();
  await page.locator('#cancel-turn').click();
  await expect(page.locator('.turn-card').last()).toContainText('本轮已停止');
  await expect(page.locator('#send')).toBeVisible();
});

test('opens a project file from the tree', async ({ page }) => {
  await openProject(page);
  await page.locator('.tree-file', { hasText: 'PROMPT.md' }).click();
  await expect(page.locator('#file-content')).toContainText('整片制作提示词');
});

test('picks the orchestrator and renderer per session', async ({ page }) => {
  await openProject(page);
  await expect(page.locator('#agent-tool')).toHaveValue('claude');
  await expect(page.locator('#agent-tool option[value="claude"]')).toContainText('默认');

  // 新对话：选的渲染工具在建会话时带上。
  await page.locator('#agent-renderer').selectOption('codex');
  await page.locator('#prompt').fill('把镜头提示词改成 after');
  await page.locator('#send').click();
  await expect(page.locator('.turn-card')).toContainText('本轮完成');
  const sessions = await (await page.request.get('/api/sessions')).json();
  expect(sessions.sessions[0]).toMatchObject({ tool: 'claude', renderer: 'codex' });

  // 已有会话：换编排工具从下一轮起生效，并在对话里留一条提示。
  await page.locator('#agent-tool').selectOption('codex');
  await expect(page.locator('#messages')).toContainText('编排工具改为 codex');
  await expect(page.locator('#agent-tool')).toHaveValue('codex');
});
