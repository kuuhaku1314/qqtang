// Run with: node --test internal/server/gm/web_app_test.cjs
// Execute the real frontend against an in-memory API with controlled responses.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const script = fs.readFileSync(path.join(__dirname, "web/app.js"), "utf8");
const clone = (value) => JSON.parse(JSON.stringify(value));

function element() {
  let text = "", html = "";
  const classes = new Set();
  return {
    value: "", checked: false, disabled: false, inert: false, options: [{}], dataset: {},
    style: { setProperty() {} },
    classList: {
      add(...values) { values.forEach((value) => classes.add(value)); },
      remove(...values) { values.forEach((value) => classes.delete(value)); },
      toggle(value, enabled) { if (enabled) classes.add(value); else classes.delete(value); },
      contains(value) { return classes.has(value); },
    },
    set textContent(value) {
      text = String(value);
      html = text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    },
    get textContent() { return text; },
    set innerHTML(value) { html = value; },
    get innerHTML() { return html; },
    add(value) { this.options.push(value); },
    addEventListener() {}, setAttribute() {}, focus() {}, select() {}, close() {}, showModal() {},
    reportValidity() { return true; },
  };
}

function fixture() {
  const nodes = new Map();
  const node = (id) => {
    if (!nodes.has(id)) nodes.set(id, element());
    return nodes.get(id);
  };
  const account = (uin, nickname, quantity) => ({
    uin,
    profile: {
      nickname, player_id: uin, gender: 0, identity: 0, tutorial_completed: true,
      inventory: [{ id: 100, quantity }],
      game_info: { degree: 1, points: 0, extended_points: 0, money: 10, wins: 0, losses: 0, draws: 0 },
    },
    inventory_items: [], loadouts: [], pets: [],
  });
  const stored = { 1001: account(1001, "Account A", 33), 1002: account(1002, "Account B", 63) };
  const summaries = () => Object.values(stored).map((value) => ({
    uin: value.uin, nickname: value.profile.nickname, degree: 1,
    inventory_count: value.profile.inventory.length,
  }));
  const holds = [], requests = [];
  function holdNext(method, url) {
    let resolve, reject, started;
    const hold = { method, url, used: false, promise: new Promise((yes, no) => { resolve = yes; reject = no; }) };
    hold.started = new Promise((yes) => { started = yes; });
    hold.markStarted = started;
    hold.release = () => { assert(hold.used, "Request must reach the mock first"); resolve(hold.response); };
    hold.fail = () => { assert(hold.used, "Request must reach the mock first"); reject(new Error("mock request failed")); };
    holds.push(hold);
    return hold;
  }
  const context = vm.createContext({
    console, AbortController, Map, WeakMap,
    Option: function (text, value) { this.text = text; this.value = value; },
    confirm: () => true, setTimeout: () => 1, clearTimeout() {},
    document: { getElementById: node, createElement: element, querySelectorAll: () => [], querySelector: (selector) => selector === ".app" ? node("app-root") : null },
    fetch: async (url, options = {}) => {
      const method = options.method || "GET", body = options.body ? JSON.parse(options.body) : undefined;
      requests.push({ method, url, body });
      let data;
      if (url === "/gm/api/accounts" && method === "POST") {
        stored[body.uin] = account(body.uin, body.nickname, 1);
        data = stored[body.uin];
      } else if (url === "/gm/api/accounts") data = { accounts: summaries() };
      else if (url.startsWith("/gm/api/pets?")) data = { pets: [] };
      else if (url.startsWith("/gm/api/items?")) data = { items: [], kinds: [], total: 0 };
      else {
        const match = url.match(/^\/gm\/api\/accounts\/(\d+)(.*)$/);
        assert(match, `Unknown URL ${url}`);
        const value = stored[match[1]], suffix = match[2];
        assert(value, "Account must exist");
        if (method === "PUT" && suffix.startsWith("/inventory/")) {
          value.profile.inventory.find((item) => item.id === 100).quantity = body.quantity;
        } else if (method === "DELETE" && suffix.startsWith("/inventory/")) value.profile.inventory = [];
        else if (method === "PUT" && suffix.startsWith("/pets/")) value.pets.push({ pet_id: 1, pet_type_id: 1, level: 1, state: 0 });
        else if (method === "DELETE" && suffix.startsWith("/pet-instances/")) value.pets = [];
        else if (method === "DELETE" && suffix === "") delete stored[match[1]];
        else if (method === "PUT" && suffix === "/password") value.testPassword = body.password;
        else if (method === "PUT" && suffix === "") {
          Object.assign(value.profile, { nickname: body.nickname, gender: body.gender, identity: body.identity, tutorial_completed: body.tutorial_completed });
          Object.assign(value.profile.game_info, { degree: body.degree, points: body.points, extended_points: body.adventure_points, money: body.money, wins: body.wins, losses: body.losses, draws: body.draws });
        } else assert(method === "GET" && suffix === "", "Unsupported request");
        data = value;
      }
      const snapshot = clone(data), response = { status: 200, ok: true, json: async () => clone(snapshot) };
      const hold = holds.find((entry) => !entry.used && entry.method === method && entry.url === url);
      if (hold) { hold.used = true; hold.response = response; hold.markStarted(); return hold.promise; }
      return response;
    },
  });
  vm.runInContext(script.replace(/start\(\)\.catch\(reportError\);\s*$/, "") +
    "\nglobalThis.app = {state, selectAccount, updateInventory, removeInventory, grantPet, removePet, grantItem, confirmGrantItem, createAccount, deleteAccount, saveProfile, setProfileDirty};", context);
  const app = context.app;
  app.state.accounts = summaries();
  const edit = (id, value) => { node(id).value = value; app.setProfileDirty(true); };
  return { app, node, stored, requests, holdNext, edit };
}

test("a write completes before any account switch or duplicate write is accepted", async () => {
  const { app, node, stored, requests, holdNext } = fixture();
  await app.selectAccount(1001);
  const hold = holdNext("PUT", "/gm/api/accounts/1001/inventory/100");
  const write = app.updateInventory(100, 34);
  assert.equal(app.state.accountBusy, true);
  assert.equal(node("app-root").inert, true);
  assert.equal(await app.selectAccount(1002), false);
  assert.equal(await app.updateInventory(100, 99), false);
  assert.equal(requests.filter((entry) => entry.method === "PUT").length, 1);
  hold.release(); await write;
  assert.equal(app.state.accountBusy, false);
  assert.equal(node("app-root").inert, false);
  await app.selectAccount(1002);
  await app.updateInventory(100, app.state.account.profile.inventory[0].quantity + 1);
  assert.equal(stored[1001].profile.inventory[0].quantity, 34);
  assert.equal(stored[1002].profile.inventory[0].quantity, 64);
});

test("selection is committed with its form only after loading succeeds", async () => {
  const { app, node, stored, requests, holdNext } = fixture();
  await app.selectAccount(1001);
  const readHold = holdNext("GET", "/gm/api/accounts/1002");
  const selection = app.selectAccount(1002);
  assert.equal(app.state.selectedUIN, 1001);
  assert.equal(app.state.account.uin, Number(node("profile-uin").value));
  assert.equal(app.state.account.uin, 1001);
  assert.equal(node("app-root").inert, true);
  assert.equal(await app.saveProfile(), false);
  assert.equal(await app.updateInventory(100, 34), false);
  assert.equal(await app.selectAccount(1001), false);
  assert.equal(requests.filter((entry) => entry.method === "PUT").length, 0);
  assert.equal(stored[1002].profile.nickname, "Account B");
  readHold.release(); await selection;
  assert.equal(app.state.account.uin, 1002);
  assert.equal(Number(node("profile-uin").value), 1002);
});

test("duplicate refreshes cannot leave a late response to erase a new draft", async () => {
  const { app, node, requests, holdNext, edit } = fixture();
  await app.selectAccount(1001);
  const first = holdNext("GET", "/gm/api/accounts/1001");
  const oldRefresh = app.selectAccount(1001);
  assert.equal(await app.selectAccount(1001), false);
  assert.equal(requests.filter((entry) => entry.url === "/gm/api/accounts/1001").length, 2);
  assert.equal(node("app-root").inert, true);
  first.release(); assert.equal(await oldRefresh, true);
  edit("profile-nickname", "new draft");
  assert.equal(node("profile-nickname").value, "new draft");
  assert.equal(app.state.profileDirty, true);
});

test("an inventory save preserves the profile draft already on screen", async () => {
  const { app, node, holdNext, edit } = fixture();
  await app.selectAccount(1001);
  edit("profile-nickname", "unsaved name");
  const hold = holdNext("PUT", "/gm/api/accounts/1001/inventory/100");
  const write = app.updateInventory(100, 34);
  assert.equal(node("app-root").inert, true);
  hold.release(); await write;
  assert.equal(node("profile-nickname").value, "unsaved name");
  assert.equal(app.state.profileDirty, true);
});

test("profile, password and summary requests remain one locked operation", async () => {
  const { app, node, stored, requests, holdNext, edit } = fixture();
  await app.selectAccount(1001);
  edit("profile-nickname", "submitted name");
  edit("profile-password", "savedpass");
  const hold = holdNext("PUT", "/gm/api/accounts/1001");
  const passwordHold = holdNext("PUT", "/gm/api/accounts/1001/password");
  const listHold = holdNext("GET", "/gm/api/accounts");
  const save = app.saveProfile();
  assert.equal(node("app-root").inert, true);
  assert.equal(await app.selectAccount(1002), false);
  hold.release(); await passwordHold.started;
  assert.equal(app.state.accountBusy, true);
  assert.equal(await app.selectAccount(1002), false);
  passwordHold.release(); await listHold.started;
  assert.equal(app.state.accountBusy, true);
  assert.equal(await app.saveProfile(), false);
  listHold.release(); await save;
  assert.equal(stored[1001].profile.nickname, "submitted name");
  assert.equal(stored[1001].testPassword, "savedpass");
  assert.equal(node("profile-nickname").value, "submitted name");
  assert.equal(node("profile-password").value, "");
  assert.equal(app.state.profileDirty, false);
  assert.equal(stored[1002].testPassword, undefined);
  assert.equal(requests.filter((entry) => entry.url.endsWith("/password")).length, 1);
  assert.equal(node("app-root").inert, false);
});

test("failed account requests unlock the UI and keep the previous selection", async () => {
  const { app, node, holdNext } = fixture();
  await app.selectAccount(1001);
  const hold = holdNext("GET", "/gm/api/accounts/1002");
  const selection = app.selectAccount(1002);
  hold.fail(); await assert.rejects(selection, /mock request failed/);
  assert.equal(app.state.account.uin, 1001);
  assert.equal(app.state.selectedUIN, 1001);
  assert.equal(Number(node("profile-uin").value), 1001);
  assert.equal(app.state.accountBusy, false);
  assert.equal(node("app-root").inert, false);
  assert.equal(await app.selectAccount(1002), true);
});

test("a failed password request preserves the form and releases the lock", async () => {
  const { app, node, holdNext, edit } = fixture();
  await app.selectAccount(1001);
  edit("profile-nickname", "draft");
  edit("profile-password", "savedpass");
  const hold = holdNext("PUT", "/gm/api/accounts/1001/password");
  const save = app.saveProfile();
  await hold.started; hold.fail();
  await assert.rejects(save, /mock request failed/);
  assert.equal(app.state.profileDirty, true);
  assert.equal(node("profile-password").value, "savedpass");
  assert.equal(node("app-root").inert, false);
  assert.equal(app.state.accountBusy, false);
});

test("create and delete can reload the account list inside their operation", async () => {
  const { app, node, stored } = fixture();
  await app.selectAccount(1001);
  node("create-uin").value = "1003";
  node("create-nickname").value = "new account";
  node("create-password").value = "savedpass";
  await app.createAccount();
  assert.equal(app.state.selectedUIN, 1003);
  assert.equal(Number(node("profile-uin").value), 1003);
  await app.deleteAccount();
  assert.equal(stored[1003], undefined);
  assert.equal(app.state.selectedUIN, 1001);
  assert.equal(app.state.accountBusy, false);
});

for (const [name, method, suffix, invoke] of [
  ["remove inventory", "DELETE", "/inventory/100", (app) => app.removeInventory(100)],
  ["grant pet", "PUT", "/pets/1", (app) => app.grantPet(1, "test pet")],
  ["remove pet", "DELETE", "/pet-instances/1", (app) => app.removePet(1)],
  ["grant inventory", "PUT", "/inventory/100", (app) => { app.grantItem(100, "test item"); return app.confirmGrantItem(); }],
]) test(`${name} also locks account switches and both dialogs`, async () => {
  const { app, node, holdNext } = fixture();
  await app.selectAccount(1001);
  const hold = holdNext(method, `/gm/api/accounts/1001${suffix}`);
  const write = invoke(app);
  assert.equal(node("app-root").inert, true);
  assert.equal(node("create-dialog").inert, true);
  assert.equal(node("grant-item-dialog").inert, true);
  assert.equal(await app.selectAccount(1002), false);
  hold.release(); await write;
  assert.equal(app.state.account.uin, 1001);
  assert.equal(node("create-dialog").inert, false);
  assert.equal(node("grant-item-dialog").inert, false);
});
