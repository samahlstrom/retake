// Retake's menu-bar item on macOS (spec/setup.md, "retake start"). Retake.app's program (the retake binary) starts it
// with osascript once its server listens:
//
//   osascript -l JavaScript menubar.js <retake pid> <url> <retake binary>
//
// It shows an icon in the menu bar with Open Retake, Start at login and Quit Retake, and goes away when Retake does.
// JavaScript for Automation ships with every Mac, so there's nothing to compile. UNTESTED on a real Mac.
ObjC.import('Cocoa');
ObjC.import('stdlib');

function run(argv) {
  const pid = parseInt(argv[0], 10);
  const url = argv[1];
  const retake = argv[2];

  // Runs retake with args and returns what it printed (retake autostart prints on or off).
  function retakeSays(args) {
    const task = $.NSTask.alloc.init;
    const pipe = $.NSPipe.pipe;
    task.launchPath = retake;
    task.arguments = args;
    task.standardOutput = pipe;
    task.launch;
    task.waitUntilExit;
    const data = pipe.fileHandleForReading.readDataToEndOfFile;
    return ObjC.unwrap($.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding)).trim();
  }
  function retakeAlive() {
    const task = $.NSTask.alloc.init;
    task.launchPath = '/bin/kill';
    task.arguments = ['-0', String(pid)];
    task.launch;
    task.waitUntilExit;
    return task.terminationStatus === 0;
  }
  function openPage() {
    $.NSWorkspace.sharedWorkspace.openURL($.NSURL.URLWithString(url));
  }

  let loginItem;
  ObjC.registerSubclass({
    name: 'RetakeMenu',
    methods: {
      'open:': { types: ['void', ['id']], implementation: () => openPage() },
      'login:': {
        types: ['void', ['id']],
        implementation: () => {
          const now = retakeSays(['autostart', loginItem.state === $.NSControlStateValueOn ? 'off' : 'on']);
          loginItem.state = now === 'on' ? $.NSControlStateValueOn : $.NSControlStateValueOff;
        },
      },
      'quit:': {
        types: ['void', ['id']],
        implementation: () => {
          // Retake stops (its agents keep working, docs/agents.md); this item goes with it.
          const task = $.NSTask.alloc.init;
          task.launchPath = '/bin/kill';
          task.arguments = ['-TERM', String(pid)];
          task.launch;
          task.waitUntilExit;
          $.NSApp.terminate(null);
        },
      },
      'tick:': {
        types: ['void', ['id']],
        implementation: () => {
          if (!retakeAlive()) $.NSApp.terminate(null);
        },
      },
    },
  });

  const app = $.NSApplication.sharedApplication;
  app.setActivationPolicy($.NSApplicationActivationPolicyAccessory); // a menu-bar item, no Dock icon
  const target = $.RetakeMenu.alloc.init;

  const item = $.NSStatusBar.systemStatusBar.statusItemWithLength($.NSVariableStatusItemLength);
  const image = $.NSImage.imageWithSystemSymbolNameAccessibilityDescription('play.rectangle', 'Retake');
  if (image && !image.isNil()) {
    image.template = true;
    item.button.image = image;
  } else {
    item.button.title = 'Retake';
  }

  const menu = $.NSMenu.alloc.initWithTitle('Retake');
  const add = (title, action, key) => {
    const m = $.NSMenuItem.alloc.initWithTitleActionKeyEquivalent(title, action, key);
    m.target = target;
    menu.addItem(m);
    return m;
  };
  add('Open Retake', 'open:', 'o');
  menu.addItem($.NSMenuItem.separatorItem);
  loginItem = add('Start at login', 'login:', '');
  loginItem.state = retakeSays(['autostart']) === 'on' ? $.NSControlStateValueOn : $.NSControlStateValueOff;
  menu.addItem($.NSMenuItem.separatorItem);
  add('Quit Retake', 'quit:', 'q');
  item.menu = menu;

  // Gone with Retake: check every 3 s that it still runs.
  $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(3, target, 'tick:', null, true);
  app.run;
}
