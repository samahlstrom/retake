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

test('opens the latest version with done feedback separate and lets an older cut stay selected', async ({ page }) => {
  await expect.poll(() => page.evaluate(() => window.__review.view.shown)).toBe('v6');
  await expect(page.locator('#verBtn')).toHaveText('v6');
  await expect(page.locator('#side [data-part~="version-picker"]')).toHaveCount(0); // one version menu: the bar's
  await expect(page.locator('#side .item')).toHaveCount(1);
  await page.getByRole('tab', {name:'Done 1', exact:true}).click();
  await expect(page.locator('#side .item .status')).toHaveText('Done');
  await expect(page.locator('#side .item .change')).toContainText('Pause shortened.');
  await page.locator('#verBtn').click();
  await page.locator('[data-ver="v5"]').click();
  await expect.poll(() => page.evaluate(() => window.__review.view.shown)).toBe('v5');
  await page.waitForTimeout(1500);
  await page.keyboard.press('[');
  expect(await page.evaluate(() => window.__review.view.shown)).toBe('v5'); // not a key
  // C compares the pinned cut with the newer one, and comes back to it.
  await page.keyboard.press('c');
  await expect.poll(() => page.evaluate(() => window.__review.view)).toMatchObject({ base: 'v5', shown: 'v6', flipped: true, switching: false });
  await page.keyboard.press('c');
  await expect.poll(() => page.evaluate(() => window.__review.view)).toMatchObject({ base: 'v5', shown: 'v5', flipped: false, switching: false });
});

test('search behind its icon and the two tabs work without firing player shortcuts', async ({ page }) => {
  await expect(page.getByRole('searchbox')).toHaveCount(0);
  await page.getByRole('button', {name:'Search comments', exact:true}).click();
  await expect(page.getByRole('searchbox')).toBeFocused();
  await page.keyboard.type('read');
  await expect(page.locator('#side .item')).toHaveCount(1);
  expect(await page.evaluate(() => window.__review.seg)).toBeNull();
  await page.getByRole('searchbox').fill('nothing like this');
  await expect(page.locator('#side .empty')).toContainText('No comments match');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('searchbox')).toHaveCount(0);
  await expect(page.locator('#side .item')).toHaveCount(1);
  await page.getByRole('tab', {name:'Done 1', exact:true}).click();
  await expect(page.locator('#side .item')).toHaveCount(1);
  await expect(page.locator('#side .item')).toContainText('Pause shortened.');
  await page.getByRole('tab', {name:'To do 1', exact:true}).click();
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
  await expect(page.locator('#side')).toBeVisible(); // always there in portrait, no bubble over the video
  await expect(page.locator('#sideToggle')).toHaveCount(0);
  await expect(page.locator('#side .item')).toHaveCount(1);
  const bounds = await page.locator('#side').boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await page.locator('.comment-marker').first().click();
  await expect.poll(() => page.locator('video').evaluate((v: HTMLVideoElement) => v.currentTime)).toBeCloseTo(3, 1);
});


test('recorded audio and drawings fold into one comment marker once transcribed', async ({ page, context }) => {
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
  // One row of markers: the comment's marker stands for its pen ticks too.
  expect(beforeInk).toBeGreaterThan(0);
  await expect.poll(async () => (await pixels()).ink).toBe(0);
  const markerBox = (await marker.boundingBox())!;
  const timelineBox = (await page.locator('#timeline').boundingBox())!;
  expect(markerBox.y + markerBox.height).toBeLessThan(timelineBox.y + timelineBox.height/2);
  await marker.hover();
  await expect(page.locator('#tip')).toContainText('Freshly transcribed comment');
  await page.keyboard.press('Tab');
});

test('Send with nothing new says the feedback is saved; one line over it says why the agent can\'t edit', async ({page}) => {
  // This server's own worker can't edit (e2e turns agent discovery off): Send saves, and never says Finish review.
  await expect(page.locator('#agent, [data-part~="agent-menu"]')).toHaveCount(0); // no agent menu in the bar
  await expect(page.locator('[data-part~="send-notice"]')).toContainText('No agent found'); // e2e finds no agents
  await expect(page.locator('#done')).toHaveText('Send');
  await page.locator('#done').click();
  await expect(page.locator('#toast')).toContainText('feedback is already saved');
  await expect(page.locator('#toast')).not.toContainText('Nothing recorded');
  await expect(page.locator('#side .item')).toHaveCount(1);
  await expect(page.locator('#side .item .status')).toHaveText('Open');
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

test('an earlier version\'s open comment stays on it: the line goes there, its drawing shows, its audio stays gone', async ({page}) => {
  const v = (r: Route) => new URL(r.request().url()).searchParams.get('v');
  await page.route('**/api/notes?*', r => json(r, {version:v(r),newest:v(r)==='v6',scope:'video',items:[{id:9,version:'v5',take:'old-take',seg:'S1',kind:'note',status:'open',text:'Move this title.',t0:3,t1:3,drawings:['red']}],jobs:[]}));
  await page.route('**/api/history?*', r => json(r, {label:v(r),history:v(r)!=='v5'?[]:[{id:'old-take',transcribed:true,voice:{bin:0.25,bins:[[12,1],[13,1],[14,1],[15,1],[16,1]]},strokes:[{id:1,video_t:3,color:'red',points:[[0.3,0.3],[0.6,0.6]]}]}]}));
  await page.reload(); await page.waitForFunction(READY);
  // Not on v6: the newest version has none of its own; the version menu's dot and title say v5 still has one.
  await expect(page.locator('#side .item')).toHaveCount(0);
  await expect(page.locator('.comment-marker')).toHaveCount(0);
  const menu = page.locator('#verBtn');
  await expect(menu).toHaveAttribute('title', /Open on earlier versions: v5 · 1/);
  await expect(menu).toHaveClass(/\bearlier\b/);
  await menu.click();
  await expect(page.locator('[data-part~="version-list"] [data-version="v5"]')).toContainText('1 to do');
  await page.locator('[data-part~="version-list"] [data-version="v5"]').click();
  await expect.poll(() => page.evaluate(() => window.__review.view.shown)).toBe('v5');
  await expect(page.locator('#side .item')).toHaveCount(1);
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

test('a failed save on Send retries without claiming success', async ({page,context}) => {
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

test('Send during recorder startup stops the pending encoder and saves its last bytes', async ({page,context}) => {
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
  const del = async (key: string) => {
    const card = page.locator(`.item[data-key="${key}"]`);
    await card.hover();
    await card.getByRole('button',{name:'More for this comment',exact:true}).click();
    await page.getByRole('menuitem',{name:'Delete',exact:true}).click();
  };
  await expect(blank.locator('.text')).toHaveText('No words');
  await del('note:90');
  await expect(blank).toHaveCount(0);
  await expect(page.locator('.item[data-key="note:1"]')).toBeVisible();
  await page.getByRole('button',{name:'Undo',exact:true}).click();
  await expect(blank).toBeVisible();
  await del('note:1');
  await expect(page.locator('.item[data-key="note:1"]')).toHaveCount(0);
  await page.reload(); await page.waitForFunction(READY);
  await expect(page.locator('.item[data-key="note:1"]')).toHaveCount(0);
  await expect(blank).toBeVisible();
  await page.getByRole('tab',{name:'Done 1',exact:true}).click();
  await expect(page.locator('.item[data-key="note:2"]')).toBeVisible();
});

test('comment menu keys open and dismiss without recording or playing', async ({page, context}) => {
  const more=page.locator('.item[data-key="note:1"]').getByRole('button',{name:'More for this comment',exact:true});
  await more.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('r');
  await page.keyboard.press(' ');
  expect(await page.evaluate(()=>!!window.__review.seg)).toBe(false);
  expect(await page.locator('video').evaluate((v:HTMLVideoElement)=>v.paused)).toBe(true);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await expect(page.locator('.item[data-key="note:1"]')).toBeVisible();
  await context.grantPermissions(['microphone'], { origin: server.url });
  await page.route('**/api/session/*/transcribe?*', r => json(r, { ok: true }));
  await page.locator('#rec').click();
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.locator('.item[data-key="note:1"]').hover();
  await more.click();
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  expect(await page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.locator('#rec').click();
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(false);
  // Clicking a player control releases menu focus, so the record shortcut works again.
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(true);
  await page.keyboard.press('r');
  await expect.poll(() => page.evaluate(() => !!window.__review.seg)).toBe(false);
});

test('Send never names the agent; held, it lists the computer\'s agents and the pick goes with that send', async ({page}) => {
  const sent: Record<string, unknown>[] = [];
  await page.route('**/api/automation',route=>json(route,{
    local:true, config:{worker:'machine',agent:'claude',fallback:'',enabled:false},
    workers:[{id:'machine',name:'Editing computer',seen:Date.now()/1000,agents:[{id:'claude',name:'Claude Code'},{id:'codex',name:'Codex'}]}],
    tasks:[],renders:[],
  }));
  await page.route('**/api/automation/tasks',route=>{ sent.push(JSON.parse(route.request().postData()!)); return json(route,{ok:true}); });
  await page.reload();await page.waitForFunction(READY);
  const send = page.locator('#done');
  await expect(send).toHaveText('Send');
  await expect(send).toHaveAttribute('aria-label', 'Send 1 comment to the agent');
  await expect(page.locator('#bar')).not.toContainText('Claude');
  await expect(page.locator('#agent')).toHaveCount(0);
  // A right-click (or holding it) lists the agents; the configured one says so. Nothing is sent yet.
  await send.click({button:'right'});
  const picker = page.locator('[data-part~="agent-picker"]');
  await expect(picker.locator('[data-agent]')).toHaveText([/Claude Code\s*default/, /^Codex$/]);
  expect(sent).toEqual([]);
  await picker.locator('[data-agent="codex"]').click();
  await expect.poll(() => sent).toEqual([{notes:[1], agent:'codex'}]);
  await expect(picker).toHaveCount(0);
});

// A fake host for the agent: GET api/automation from `state`; a Send queues tasks and the notes carry them (`work`).
async function fakeAgent(page: import('@playwright/test').Page, opts: { local: boolean; role?: string }) {
  const state = {
    workers: [] as unknown[],
    tasks: [] as Record<string, unknown>[],
    submitted: [] as unknown[],
    retried: [] as unknown[],
    minted: 0,
  };
  await page.route('**/api/automation', route => json(route, {
    local: opts.local, config: {worker:'machine',agent:'claude',fallback:'',enabled:false},
    workers: state.workers, tasks: state.tasks, renders: [], sending: [],
  }));
  await page.route('**/api/automation/tasks', route => {
    const body = JSON.parse(route.request().postData()!) as { notes: number[] };
    state.submitted.push(body);
    for (const id of body.notes)
      state.tasks.push({id:`t${id}`, notes:[id], version:'v6', state:'queued', agent:'', worker:'', attempt:0, events:[{at:1,text:'Feedback submitted'}]});
    return json(route, {ok:true});
  });
  await page.route('**/api/automation/tasks/*/retry', route => {
    state.retried.push(JSON.parse(route.request().postData()!));
    const id = new URL(route.request().url()).pathname.split('/').at(-2);
    const t = state.tasks.find(t => t.id === id)!;
    t.state = 'queued'; t.error = '';
    return json(route, {ok:true});
  });
  await page.route('**/api/notes?*', route => json(route, {version:'v6', newest:true, scope:'review', jobs:[],
    items: items.map(n => ({...n, work: state.tasks.find(t => (t.notes as number[]).includes(n.id))}))}));
  await page.route('**/api/connect', route => { state.minted++; return json(route, {command:'retake connect https://review.example.com --token rk_test'}); });
  if (opts.role)
    await page.route('**/api/info', async route => {
      const real = await route.fetch();
      return json(route, {...(await real.json()), role: opts.role});
    });
  await page.reload(); await page.waitForFunction(READY);
  return state;
}

test('the owner sends with no computer: the comments wait for it, and one line says none is connected', async ({page}) => {
  const agent = await fakeAgent(page, {local:false});
  const notice = page.locator('[data-part~="send-notice"]');
  await expect(notice).toContainText('No editing computer connected'); // connecting one is in Settings now
  await expect(page.locator('#done')).toHaveText('Send');
  await page.locator('#done').click();
  await expect.poll(() => agent.submitted).toEqual([{notes:[1]}]);
  await expect(page.locator('#toast')).toContainText('The agent starts when the editing computer is online');
  const card = page.locator('.item[data-key="note:1"]');
  await expect(card.locator('.status')).toHaveText('Working');
  await expect(card.locator('.waiting')).toHaveText('Waiting for the editing computer');
  await expect(page.locator('#done')).toHaveText('Sent');
  expect(agent.minted).toBe(0); // the review page mints no connect token: Settings does
  agent.workers.push({id:'machine',name:'Editing computer',seen:Date.now()/1000,online:true,agents:[{id:'claude',name:'Claude Code'}]});
  // The page polls api/automation every 5 s: the computer shows up without a reload, and the lines go.
  await expect(notice).toHaveCount(0, {timeout:12_000});
  await expect(card.locator('.status')).toHaveText('Working');
  await expect(card.locator('.waiting')).toHaveCount(0);
});

test('a reviewer\'s link sends like the owner; with no computer it just waits, and a paused send retries', async ({page}) => {
  const agent = await fakeAgent(page, {local:false, role:'reviewer'});
  await expect(page.locator('[data-part~="send-notice"]')).toHaveText('The editing computer is offline. Send still queues.');
  await expect(page.locator('#done')).toHaveText('Send');
  await page.locator('#done').click();
  await expect.poll(() => agent.submitted).toEqual([{notes:[1]}]);
  await expect(page.locator('#toast')).toContainText('The agent starts when the editing computer is online');
  const card = page.locator('.item[data-key="note:1"]');
  await expect(card.locator('.status')).toHaveText('Working');
  await expect(card.locator('.waiting')).toHaveText('Waiting for the editing computer');
  // No connect line for a reviewer, and no Settings link: the line only says what's happening.
  await expect(page.locator('[data-part~="send-notice"] a')).toHaveCount(0);
  expect(agent.minted).toBe(0);
  // The computer went away mid-task: the comment is Open with why, and the reviewer retries it (with its own agent).
  Object.assign(agent.tasks[0]!, {state:'paused', error:'Worker disconnected; retry or switch agent'});
  await expect(card.locator('.paused')).toContainText('Paused: computer offline', {timeout:12_000}); // next poll
  await card.getByRole('button',{name:'Retry',exact:true}).click();
  await expect.poll(() => agent.retried).toEqual([{}]);
  await card.hover();
  await card.getByRole('button',{name:'More for this comment',exact:true}).click();
  await expect(page.getByRole('menuitem',{name:'Pause',exact:true})).toHaveCount(0);
  await expect(page.getByRole('menuitem',{name:'History',exact:true})).toBeVisible();
});

test('the keyboard hint shows once per browser; ? opens the keys', async ({page}) => {
  await page.evaluate(() => localStorage.removeItem('retake.hint'));
  await page.reload();await page.waitForFunction(READY);
  await expect(page.locator('#hint')).not.toHaveClass(/gone/);
  await page.keyboard.press('?');
  await expect(page.locator('.keys-pop')).toContainText('Compare versions');
  await expect(page.locator('#hint')).toHaveClass(/gone/);
  await page.keyboard.press('Escape');
  await expect(page.locator('.keys-pop')).toHaveCount(0);
  await page.reload();await page.waitForFunction(READY);
  await page.waitForTimeout(500);
  await expect(page.locator('#hint')).toHaveClass(/gone/);
  await page.locator('#keys').click();
  await expect(page.locator('.keys-pop')).toBeVisible();
});
