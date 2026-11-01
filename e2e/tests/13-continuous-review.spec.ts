import { expect, test, type Route } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { family } from '../lib/work.ts';
import { startServer, type Server } from '../lib/server.ts';
import { READY, Review, line } from '../lib/review.ts';

test.skip(LEGACY, 'continuous review is the current player');
let server: Server;
const items = [
  { id: 1, kind: 'note', status: 'open', t0: 3, t1: 5, text: 'Make this title easier to read.', by: 'Alex' },
  { id: 2, kind: 'note', status: 'answered', t0: 10, t1: 11, text: 'Trim this pause.', by: 'Sam', change: 'Pause shortened.', answered_in: 'v6' },
];
const json = (r: Route, body: unknown) => r.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
test.beforeAll(async () => {
  const f = family('continuous-review', ['v5', 'v6']);
  server = await startServer('continuous-review', f.files.v5!, PORTS.sidebar);
});
test.afterAll(async () => { await server?.stop(); });
test.beforeEach(async ({ page }) => {
  await page.route('**/api/notes?*', r => json(r, { version: 'v6', newest: true, scope: 'review', items, jobs: [] }));
  await page.route('**/api/events*', r => r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 60000\n\n' }));
  await page.goto(server.url);
  await page.waitForFunction(READY);
});

test('opens the latest version with resolved feedback separate and lets an older cut stay selected', async ({ page }) => {
  await expect.poll(() => page.evaluate(() => window.__review.view.shown)).toBe('v6');
  await expect(page.locator('#verBtn')).toHaveText('v6⌄');
  await expect(page.locator('#side header')).toContainText('Comments');
  await expect(page.locator('#side .item')).toHaveCount(1);
  await page.getByRole('button', {name:'Resolved 1', exact:true}).click();
  await expect(page.locator('#side .resolved')).toContainText('Resolved in v6');
  await page.locator('#verBtn').click();
  await page.locator('[data-ver="v5"]').click();
  await expect.poll(() => page.evaluate(() => window.__review.view.shown)).toBe('v5');
  await page.waitForTimeout(1500);
  await page.keyboard.press('c');
  await page.keyboard.press('[');
  expect(await page.evaluate(() => window.__review.view.shown)).toBe('v5');
});

test('search and status filters work without firing player shortcuts', async ({ page }) => {
  await page.getByRole('searchbox').fill('read');
  await expect(page.locator('#side .item')).toHaveCount(1);
  expect(await page.evaluate(() => window.__review.seg)).toBeNull();
  await page.getByRole('searchbox').fill('');
  await page.getByRole('combobox', {name:'Filter comments',exact:true}).click();
  await page.getByRole('option', {name:'Resolved',exact:true}).click();
  await expect(page.locator('#side .item')).toHaveCount(1);
  await expect(page.locator('#side .item')).toContainText('Pause shortened.');
  await page.getByRole('combobox', {name:'Filter comments',exact:true}).click();
  await page.getByRole('option', {name:'Open',exact:true}).click();
  await expect(page.locator('#side .item')).toContainText('title');
});

test('timeline hover, keyboard focus and clicks expose comments at their moment', async ({ page }) => {
  const marker = page.locator('.comment-marker').first();
  await marker.hover();
  await expect(page.locator('#tip')).toContainText('Make this title easier to read.');
  await page.mouse.move(0, 0);
  await marker.focus();
  await expect(page.locator('#tip')).toContainText('Alex');
  await page.keyboard.press('Enter');
  await expect.poll(() => page.locator('video').evaluate((v: HTMLVideoElement) => v.currentTime)).toBeCloseTo(3, 1);
});

test('a stopped voice clip starts before Done and retries a temporary failure', async ({ page, context }) => {
  await context.grantPermissions(['microphone'], { origin: server.url });
  const calls: { path: string; body: string | null }[] = [];
  page.on('request', r => { if (r.method() === 'POST') calls.push({ path: new URL(r.url()).pathname, body: r.postData() }); });
  let attempts = 0;
  await page.route('**/api/session/*/transcribe?*', r => ++attempts === 1 ? r.fulfill({status: 503, body: 'retry'}) : json(r, { ok: true }));
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.waitForTimeout(1200);
  expect(calls.some(c => c.path.endsWith('/transcribe'))).toBe(false);
  await page.keyboard.press('r');
  await expect.poll(() => calls.some(c => c.path.endsWith('/transcribe'))).toBe(true);
  const i = calls.findIndex(c => c.path.endsWith('/transcribe'));
  const saved = calls.slice(0, i).filter(c => c.path.endsWith('/data')).at(-1);
  expect(JSON.parse(saved!.body!).segments[0].recording).toBe(false);
  expect(calls.slice(0, i).some(c => c.path.endsWith('/audio'))).toBe(true);
  expect(calls.some(c => c.path.endsWith('/done'))).toBe(false);
  expect(await page.evaluate(() => window.__review.take.closed)).toBe(false);
  await expect.poll(() => attempts, {timeout: 15000}).toBe(2);
});

test('phone comments and timeline stay inside the viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#sideToggle')).toBeVisible();
  await page.locator('#sideToggle').click();
  await expect(page.locator('#side')).toBeVisible();
  await expect(page.locator('#side .item')).toHaveCount(1);
  const bounds = await page.locator('#side').boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await page.locator('.comment-marker').first().click();
  await expect.poll(() => page.locator('video').evaluate((v: HTMLVideoElement) => v.currentTime)).toBeCloseTo(3, 1);
});


test('recorded audio becomes comments above the bar while drawings stay below', async ({ page, context }) => {
  await context.grantPermissions(['microphone'], { origin: server.url });
  let transcribed: Record<string, unknown> | null = null;
  await page.route('**/api/notes?*', r => json(r, {version:'v6', newest:true, scope:'review', items:[...items, ...(transcribed ? [transcribed] : [])], jobs:[]}));
  await page.route('**/api/events*', r => r.fulfill({contentType:'text/event-stream', body:`retry: 300\n\nevent: notes\ndata: {}\n\n`}));
  await page.route('**/api/session/*/transcribe?*', r => json(r, {ok:true}));
  await page.reload(); await page.waitForFunction(READY);
  const ui = new Review(page);
  await page.locator('video').evaluate((v: HTMLVideoElement) => {v.currentTime = 3});
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await ui.drag(line([0.3,0.3], [0.6,0.4]));
  const pixels = () => page.locator('#tl').evaluate((canvas: HTMLCanvasElement) => {
    const {data} = canvas.getContext('2d')!.getImageData(0,0,canvas.width,canvas.height);
    let audio = 0, ink = 0;
    for (let i=0; i<data.length; i+=4) {
      const y = Math.floor(i / 4 / canvas.width);
      const x = (i / 4) % canvas.width;
      const scale = canvas.width / canvas.clientWidth;
      const at = 8 * scale + (canvas.width - 16 * scale) * 3 / document.querySelector('video')!.duration;
      if (Math.abs(x - at) < 5 * scale && y < canvas.height/2 && data[i]! < 130 && data[i+1]! > 150 && data[i+2]! > 200 && data[i+3]! > 20) audio++;
      if (y > canvas.height/2 && data[i]! > 200 && data[i+1]! < 130 && data[i+2]! < 130 && data[i+3]! > 20) ink++;
    }
    return {audio, ink};
  });
  await expect.poll(async () => (await pixels()).audio).toBeGreaterThan(0);
  await expect.poll(async () => (await pixels()).ink).toBeGreaterThan(0);
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(false);
  await expect.poll(async () => (await pixels()).audio).toBeGreaterThan(0);
  const beforeInk = (await pixels()).ink;
  const take = await page.evaluate(() => window.__review.take.id);
  transcribed = {id:3, kind:'note', status:'open', take, seg:'S1', t0:3, t1:3, text:'Freshly transcribed comment', by:'Reviewer', drawings:['red']};
  const marker = page.locator('.comment-marker[data-comment="note:3"]');
  await expect(marker).toBeVisible();
  await expect.poll(async () => (await pixels()).audio).toBe(0);
  expect((await pixels()).ink).toBe(beforeInk);
  const markerBox = (await marker.boundingBox())!;
  const timelineBox = (await page.locator('#timeline').boundingBox())!;
  expect(markerBox.y + markerBox.height).toBeLessThan(timelineBox.y + timelineBox.height/2);
  await marker.hover();
  await expect(page.locator('#tip')).toContainText('Freshly transcribed comment');
  await page.keyboard.press('Tab');
});

test('Finish review explains already saved feedback after reopening the page', async ({page}) => {
  await page.locator('#done').click();
  await expect(page.locator('#toast')).toContainText('feedback is already saved');
  await expect(page.locator('#toast')).not.toContainText('Nothing recorded');
  await expect(page.locator('#side .item')).toHaveCount(1);
});

test('long recording publishes a completed clip while the next clip keeps recording, then finishes both', async ({page, context}) => {
  await context.grantPermissions(['microphone'], {origin:server.url});
  const calls: {path:string, body:string|null}[] = [];
  let ready: Record<string, unknown> | null = null;
  page.on('request', r => { if(r.method()==='POST') calls.push({path:new URL(r.url()).pathname, body:r.postData()}); });
  await page.route('**/api/notes?*', r => json(r, {version:'v6', newest:true, scope:'review', items:ready ? [...items, ready] : items, jobs:[]}));
  await page.route('**/api/events*', r => r.fulfill({contentType:'text/event-stream', body:'retry: 300\n\nevent: notes\ndata: {}\n\n'}));
  await page.route('**/api/session/*/transcribe?*', async r => {
    const data = JSON.parse(calls.filter(c => c.path.endsWith('/data')).at(-1)!.body!);
    const clip = data.segments.find((s: {recording:boolean}) => !s.recording);
    expect(clip).toBeTruthy();
    ready = {id:30, kind:'note', status:'open', text:'First clip is transcribed while I keep talking.', t0:3,t1:3, version:'v6', take:data.session.session, seg:clip.id};
    await json(r, {ok:true});
  });
  await page.reload(); await page.waitForFunction(READY);
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => window.__review.take.segments.length), {timeout:30000}).toBeGreaterThanOrEqual(2);
  await expect(page.locator('#side .item').filter({hasText:'First clip is transcribed'})).toBeVisible();
  expect(await page.evaluate(() => !!window.__review.seg)).toBe(true);
  const state = await page.evaluate(() => window.__review.take.segments.map(s => ({id:s.id, p0:s.p0,p1:s.p1,size:s.size,finalized:s.finalized})));
  expect(state[0]!.finalized).toBe(true);
  expect(state[0]!.size).toBeGreaterThan(1000);
  expect(Math.abs(state[1]!.p0-state[0]!.p1!)).toBeLessThan(0.1);
  await page.waitForTimeout(1200);
  await page.locator('#done').click();
  await expect(page.locator('#done')).toHaveText('Saved');
  const saved = JSON.parse(calls.filter(c => c.path.endsWith('/data')).at(-1)!.body!);
  expect(saved.segments.every((s: {recording:boolean}) => !s.recording)).toBe(true);
  expect(saved.segments.every((s: {voice:unknown}) => !!s.voice)).toBe(true);
  expect(await page.evaluate(() => window.__review.take.segments.every(s => s.rec.state === 'inactive'))).toBe(true);
});

test('reloaded completed audio stays gone while original carried drawings remain visible', async ({page}) => {
  await page.route('**/api/notes?*', r => json(r, {version:'v6',newest:true,scope:'review',items:[{id:9,version:'v5',take:'old-take',seg:'S1',kind:'note',status:'open',text:'Move this title.',t0:3,t1:3,drawings:['red']}],jobs:[]}));
  await page.route('**/api/history?*', r => json(r, {label:'v6',history:[{id:'old-take',source_version:'v5',transcribed:true,voice:{bin:0.25,bins:[[12,1],[13,1],[14,1],[15,1],[16,1]]},strokes:[{id:1,video_t:3,color:'red',points:[[0.3,0.3],[0.6,0.6]]}]}]}));
  await page.reload(); await page.waitForFunction(READY);
  await page.getByRole('button',{name:'Earlier 1',exact:true}).click();
  await page.locator('#side .time').click();
  await expect.poll(() => page.locator('#ink').evaluate((canvas:HTMLCanvasElement) => {
    const d=canvas.getContext('2d')!.getImageData(0,0,canvas.width,canvas.height).data;
    let n=0; for(let i=0;i<d.length;i+=4) if(d[i]!>180&&d[i+1]!<100&&d[i+3]!>30)n++;
    return n;
  })).toBeGreaterThan(100);
  const audio = await page.locator('#tl').evaluate((canvas:HTMLCanvasElement) => {
    const d=canvas.getContext('2d')!.getImageData(0,0,canvas.width,canvas.height).data;
    let n=0; for(let i=0;i<d.length;i+=4) if(d[i]!<130&&d[i+1]!>140&&d[i+2]!>220&&d[i+3]!>30)n++;
    return n;
  });
  expect(audio).toBe(0);
  await page.locator('.comment-marker').hover();
  await expect(page.locator('#tip')).toContainText('Move this title.');
});

test('a failed Finish request retries without claiming success', async ({page,context}) => {
  await context.grantPermissions(['microphone'],{origin:server.url});
  let attempts=0;
  await page.route('**/api/session/*/done?*', r => ++attempts === 1 ? r.fulfill({status:503,body:'retry'}) : json(r,{ok:true,jobs:[]}));
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.waitForTimeout(1100);
  await page.locator('#done').click();
  await expect(page.locator('#toast')).toContainText('processing could not start');
  await expect(page.locator('#done')).not.toHaveText('Saved');
  await expect.poll(() => attempts,{timeout:12000}).toBe(2);
  await expect(page.locator('#done')).toHaveText('Saved');
});

test('Finish during recorder startup stops the pending encoder and saves its last bytes', async ({page,context}) => {
  await context.grantPermissions(['microphone'],{origin:server.url});
  await page.evaluate(() => {
    const start=MediaRecorder.prototype.start;
    MediaRecorder.prototype.start=function(timeslice?:number) {
      const callback=this.onstart;
      this.onstart=(event) => setTimeout(() => callback?.call(this,event),700);
      start.call(this,timeslice);
    };
  });
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => window.__review.take.id)).not.toBeNull();
  await page.locator('#done').click();
  await expect(page.locator('#done')).toHaveText('Saved');
  expect(await page.evaluate(() => window.__review.take.segments.every(s => s.rec.state==='inactive'&&s.finalized&&s.size>0))).toBe(true);
  expect(await page.evaluate(() => !!window.__review.seg)).toBe(false);
});

test('empty and saved comments can be deleted and undone without removing other feedback', async ({page}) => {
  const hidden = new Set<number>();
  const empty = {id:90,kind:'note',status:'open',version:'v6',t0:6,t1:7,text:'',by:'Alex'};
  await page.route('**/api/notes?*',r=>json(r,{version:'v6',newest:true,scope:'review',items:[...items,empty].filter(n=>!hidden.has(n.id)),jobs:[]}));
  await page.route('**/api/notes/*/delete',r=>{hidden.add(Number(new URL(r.request().url()).pathname.split('/')[3]));return json(r,{ok:true});});
  await page.route('**/api/notes/*/restore',r=>{hidden.delete(Number(new URL(r.request().url()).pathname.split('/')[3]));return json(r,{ok:true});});
  await page.reload(); await page.waitForFunction(READY);
  const blank=page.locator('.item[data-key="note:90"]');
  await blank.getByRole('button',{name:'Delete comment',exact:true}).click();
  await expect(blank).toHaveCount(0);
  await expect(page.locator('.item[data-key="note:1"]')).toBeVisible();
  await page.getByRole('button',{name:'Undo',exact:true}).click();
  await expect(blank).toBeVisible();
  await page.locator('.item[data-key="note:1"]').getByRole('button',{name:'Delete comment',exact:true}).click();
  await expect(page.locator('.item[data-key="note:1"]')).toHaveCount(0);
  await page.reload(); await page.waitForFunction(READY);
  await expect(page.locator('.item[data-key="note:1"]')).toHaveCount(0);
  await expect(blank).toBeVisible();
  await page.getByRole('button',{name:'Resolved 1',exact:true}).click();
  await expect(page.locator('.item[data-key="note:2"]')).toBeVisible();
});

test('comment dropdown keys select and dismiss without recording or playing', async ({page, context}) => {
  const filter=page.getByRole('combobox',{name:'Filter comments',exact:true});
  await filter.focus();
  await page.keyboard.press('Space');
  await expect(page.getByRole('listbox',{name:'Filter comments',exact:true})).toBeVisible();
  await page.keyboard.press('r');
  await page.keyboard.press('Enter');
  await expect(filter).toHaveText('Resolved');
  expect(await page.evaluate(()=>!!window.__review.seg)).toBe(false);
  expect(await page.locator('video').evaluate((v:HTMLVideoElement)=>v.paused)).toBe(true);
  await filter.click();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('listbox')).toHaveCount(0);
  await expect(filter).toBeFocused();
  await expect(page.locator('.item[data-key="note:2"]')).toBeVisible();
  await context.grantPermissions(['microphone'], { origin: server.url });
  await page.route('**/api/session/*/transcribe?*', r => json(r, { ok: true }));
  await page.locator('#rec').click();
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await filter.click();
  await page.keyboard.press('Escape');
  expect(await page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.locator('#rec').click();
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(false);
  // Clicking a player control releases menu focus, so the record shortcut works again.
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(false);
});

test('agent settings use the same menus inside their popover', async ({page}) => {
  await page.route('**/api/automation',route=>json(route,{
    config:{worker:'machine',agent:'claude',fallback:'',enabled:false},
    workers:[{id:'machine',name:'Editing computer',seen:Date.now()/1000,agents:[{id:'claude',name:'Claude Code'},{id:'codex',name:'Codex'}]}],
    tasks:[],renders:[],
  }));
  await page.reload();await page.waitForFunction(READY);
  await page.getByRole('button',{name:'Agent controls',exact:true}).click();
  const provider=page.getByRole('combobox',{name:'Default agent',exact:true});
  await expect(provider).toHaveText('Claude Code');
  await provider.click();
  await page.getByRole('option',{name:'Codex',exact:true}).click();
  await expect(provider).toHaveText('Codex');
  const fallback=page.getByRole('combobox',{name:'When usage runs out',exact:true});
  await fallback.click();
  await expect(page.getByRole('option',{name:'Switch to Claude Code',exact:true})).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button',{name:'Save agent settings',exact:true})).toBeVisible();
  await expect(fallback).toHaveText('Pause and let me choose');
});
