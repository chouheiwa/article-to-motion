import { test, expect } from '@playwright/test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

let project;

test.beforeEach(async () => {
  project = await fs.mkdtemp(path.join(os.tmpdir(), 'am-studio-e2e-'));
  const scene = path.join(project, 'scenes', 'scene-001');
  await fs.mkdir(scene, { recursive: true });
  await fs.writeFile(path.join(scene, 'voice.srt'), '1\n00:00:00,000 --> 00:00:01,000\nhello\n');
  await fs.writeFile(path.join(scene, 'scene.json'), JSON.stringify({
    id: 'scene-001', duration_seconds: 1, output: 'out.mp4', transcript: 'voice.srt', text: 'before'
  }));
});

test.afterEach(async () => { await fs.rm(project, { recursive: true, force: true }); });

test('opens a project, selects a scene, asks AI, confirms the operation, and observes the task', async ({ page }) => {
  await page.goto('/');
  await page.locator('#project-path').fill(project);
  await page.locator('#open-project').click();
  await expect(page.locator('#title')).toContainText(path.basename(project));
  await page.locator('.scene', { hasText: 'scene-001' }).click();
  await expect(page.locator('#scene-meta')).toContainText('scene-001');
  await page.locator('#prompt').fill('字幕：after');
  await page.locator('#send').click();
  await expect(page.locator('#messages')).toContainText('字幕');
  await expect(page.locator('#operation')).toBeVisible();
  await expect(page.locator('#operation')).toContainText('scene.update_caption');
  await page.locator('#confirm-op').click();
  await expect(page.locator('#messages')).toContainText('任务');
  await expect.poll(async () => page.evaluate(async () => (await (await fetch('/api/tasks')).json()).tasks.at(-1)?.status), { timeout: 10_000 }).toBe('succeeded');
  await expect.poll(async () => (await fs.readFile(path.join(project, 'scenes', 'scene-001', 'scene.json'), 'utf8'))).toContain('after');
});
