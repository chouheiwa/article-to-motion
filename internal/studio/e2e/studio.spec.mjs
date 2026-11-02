import { test, expect } from '@playwright/test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

let project;

test.beforeEach(async () => {
  project = await fs.mkdtemp(path.join(process.env.HOME || os.homedir(), 'am-studio-e2e-'));
  const scene = path.join(project, 'scenes', 'scene-001');
  await fs.mkdir(scene, { recursive: true });
  await fs.writeFile(path.join(project, 'article-to-motion.conf'), 'ORCHESTRATOR=claude\nRENDERER=claude\n');
  await fs.writeFile(path.join(scene, 'voice.srt'), '1\n00:00:00,000 --> 00:00:01,000\nhello\n');
  await fs.writeFile(path.join(scene, 'prompt.md'), 'before\n');
  await fs.writeFile(path.join(scene, 'scene.json'), JSON.stringify({
    id: 'scene-001', duration_seconds: 1, output: 'out.mp4', transcript: 'voice.srt', text: 'hello'
  }));
});

test.afterEach(async () => { await fs.rm(project, { recursive: true, force: true }); });

test('opens a project, asks for a change, sees the turn diff, and reverts it', async ({ page }) => {
  const promptFile = path.join(project, 'scenes', 'scene-001', 'prompt.md');
  await page.goto('/');
  await page.locator('#open-project-trigger').click();
  await page.locator('.directory-row.project-row', { hasText: path.basename(project) }).click();
  await page.locator('#open-project').click();
  await expect(page.locator('#title')).toContainText(path.basename(project));
  await page.locator('.scene', { hasText: 'scene-001' }).click();
  await page.locator('#prompt').fill('把镜头提示词改成 after');
  await page.locator('#send').click();
  await expect(page.locator('#messages')).toContainText('已把镜头提示词改好');
  await expect(page.locator('#operation')).toBeVisible();
  await expect(page.locator('#operation')).toContainText('scenes/scene-001/prompt.md');
  await expect.poll(async () => (await fs.readFile(promptFile, 'utf8'))).toBe('after\n');
  await page.locator('#revert-turn').click();
  await expect(page.locator('#messages')).toContainText('已回退这一轮');
  await expect.poll(async () => (await fs.readFile(promptFile, 'utf8'))).toBe('before\n');
});
