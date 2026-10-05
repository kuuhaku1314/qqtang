"use strict";

const state = {
  accounts: [], selectedUIN: 0, account: null, progression: null,
  petCatalog: [], petByType: new Map(), pendingGrantItem: null,
  tab: "profile", profileDirty: false, accountBusy: false,
  inventoryPage: 1, inventoryPageSize: 15, inventoryKind: "", inventoryQuery: "",
  itemPage: 1, itemPageSize: 60, itemTotal: 0, itemRows: [], itemAbort: null,
};
const $ = (id) => document.getElementById(id);
const itemMotionTimers = new WeakMap();
const maxQuantity = 999;

// Keep an account operation and its follow-up requests in one exclusive UI action.
// Block new actions instead of queuing clicks whose account or form may change.
// The inert roots also cover controls created by a render during the operation.
async function runAccountOperation(operation) {
  if (state.accountBusy) return false;
  state.accountBusy = true;
  const focused = document.activeElement;
  const roots = [document.querySelector(".app"), $("create-dialog"), $("grant-item-dialog")].filter(Boolean);
  for (const root of roots) {
    root.inert = true;
    root.setAttribute("aria-busy", "true");
  }
  try {
    return await operation();
  } finally {
    for (const root of roots) {
      root.inert = false;
      root.setAttribute("aria-busy", "false");
    }
    state.accountBusy = false;
    if (focused?.isConnected && document.activeElement === document.body) focused.focus({ preventScroll: true });
  }
}

function accountAction(operation) {
  return (...args) => runAccountOperation(() => operation(...args));
}

async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { "Content-Type": "application/json", ...(options.headers || {}) } });
  if (response.status === 204) return null;
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败 (${response.status})`);
  return data;
}

function toast(message, isError = false) {
  const node = $("toast");
  node.textContent = message;
  node.className = `toast${isError ? " error" : ""}`;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => node.classList.add("hidden"), isError ? 5000 : 3200);
}

function itemName(id) {
  const row = document.querySelector(`[data-item-id="${id}"]`);
  return row?.dataset.itemName || `物品 ${id}`;
}

function selectedRole() {
  // The selected character is session state supplied by REQUEST_LOGIN, not an
  // account property. GM image previews use Maria as a stable neutral model;
  // per-character equipment continues to carry its own role_id.
  return 7;
}

function itemImageURL(id, role = selectedRole(), appearance = false, animate = false) {
  return `/gm/api/items/${id}/image?role=${role}&appearance=${appearance ? 1 : 0}&animate=${animate ? 1 : 0}`;
}

// Hovering the whole row/card (not just the small icon) plays the animated
// preview, so the larger target is easier to hit.
function bindItemImageMotion() {
  document.querySelectorAll("img[data-item-image]:not([data-motion-bound])").forEach((image) => {
    image.dataset.motionBound = "1";
    image.dataset.imageAnimated = "false";
    const host = image.closest("[data-motion-host]") || image;
    const update = (animate) => {
      const next = animate ? "true" : "false";
      if (image.dataset.imageAnimated === next) return;
      image.dataset.imageAnimated = next;
      const role = Number(image.dataset.imageRole) || selectedRole();
      image.src = itemImageURL(Number(image.dataset.itemImage), role, image.dataset.imageAppearance === "true", animate);
    };
    const start = () => {
      clearTimeout(itemMotionTimers.get(image));
      itemMotionTimers.set(image, setTimeout(() => update(true), 140));
    };
    const stop = () => {
      clearTimeout(itemMotionTimers.get(image));
      itemMotionTimers.delete(image);
      update(false);
    };
    host.addEventListener("pointerenter", start);
    host.addEventListener("pointerleave", stop);
    image.addEventListener("focus", start);
    image.addEventListener("blur", stop);
  });
}

async function loadHealth() {
  const node = $("health");
  try {
    const health = await api("/gm/api/health");
    node.className = "health ok";
    node.innerHTML = `<span></span>服务正常 · ${health.item_count} 项物品 · ${health.pet_type_count} 种宠物`;
  } catch (error) {
    node.className = "health error";
    node.innerHTML = `<span></span>服务不可用`;
  }
}

async function loadProgression() {
  state.progression = await api("/gm/api/progression");
  $("profile-identity").innerHTML = state.progression.identities.map((entry) =>
    `<option value="${entry.value}">${escapeHTML(entry.label)}（${entry.value}）</option>`).join("");
  $("profile-degree").innerHTML = state.progression.competitive_ranks.map((rank) =>
    `<option value="${rank.degree}">Lv.${rank.degree} · ${escapeHTML(rank.title)} · 第${rank.sub_level}阶</option>`).join("");
  $("profile-adventure-level").innerHTML = state.progression.adventure_ranks.map((rank) =>
    `<option value="${rank.level}">探险 Lv.${rank.level}</option>`).join("");
}

function competitiveRankForPoints(points) {
  const ranks = state.progression?.competitive_ranks || [];
  return ranks.find((rank) => points >= rank.min_points && points <= rank.max_points) || ranks[ranks.length - 1];
}

function adventureRankForPoints(points) {
  const ranks = state.progression?.adventure_ranks || [];
  return ranks.find((rank) => points >= rank.min_points && points <= rank.max_points) || ranks[ranks.length - 1];
}

function showCompetitiveRank(rank) {
  if (!rank) return;
  $("profile-degree").value = rank.degree;
  $("profile-points").min = rank.min_points;
  $("profile-points").max = rank.max_points;
  $("profile-degree-detail").textContent = `${rank.title} · 主徽章 ${rank.main_level} · 子图标 ${rank.sub_level}/6`;
  $("profile-points-range").textContent = `本级范围：${rank.min_points.toLocaleString()}–${rank.max_points.toLocaleString()}`;
}

function showAdventureRank(rank) {
  if (!rank) return;
  $("profile-adventure-level").value = rank.level;
  $("profile-adventure-points").min = rank.min_points;
  $("profile-adventure-points").max = rank.max_points;
  $("profile-adventure-range").textContent = `本级范围：${rank.min_points.toLocaleString()}–${rank.max_points.toLocaleString()}`;
}

function selectCompetitiveRank() {
  const rank = state.progression.competitive_ranks.find((entry) => entry.degree === numberValue("profile-degree"));
  if (!rank) return;
  setValue("profile-points", rank.min_points);
  showCompetitiveRank(rank);
}

function deriveCompetitiveRank() {
  showCompetitiveRank(competitiveRankForPoints(numberValue("profile-points")));
}

function selectAdventureRank() {
  const rank = state.progression.adventure_ranks.find((entry) => entry.level === numberValue("profile-adventure-level"));
  if (!rank) return;
  setValue("profile-adventure-points", rank.min_points);
  showAdventureRank(rank);
}

function deriveAdventureRank() {
  showAdventureRank(adventureRankForPoints(numberValue("profile-adventure-points")));
}

async function refreshAccountList() {
  const data = await api("/gm/api/accounts");
  state.accounts = data.accounts || [];
  renderAccounts();
}

async function loadAccounts(selectUIN = state.selectedUIN) {
  await refreshAccountList();
  if (selectUIN && state.accounts.some((entry) => entry.uin === selectUIN)) await loadAccount(selectUIN);
  else if (state.accounts.length && !state.selectedUIN) await loadAccount(state.accounts[0].uin);
  else if (!state.accounts.length) showEmpty();
}

function avatarHue(uin) {
  return (Number(uin) * 47) % 360;
}

function avatarInitial(name) {
  return Array.from(String(name || "?").trim() || "?")[0];
}

function renderAccounts() {
  const query = $("account-search").value.trim().toLowerCase();
  const filtered = state.accounts.filter((account) => `${account.uin} ${account.nickname}`.toLowerCase().includes(query));
  $("account-total").textContent = state.accounts.length;
  $("account-list").innerHTML = filtered.length ? filtered.map((account) => {
    const active = account.uin === state.selectedUIN;
    return `
    <button type="button" class="account-card${active ? " active" : ""}" data-uin="${account.uin}"${active ? ` aria-current="true"` : ""}>
      <span class="avatar" style="--hue:${avatarHue(account.uin)}">${escapeHTML(avatarInitial(account.nickname))}</span>
      <span class="account-copy"><strong>${escapeHTML(account.nickname || "未命名")}</strong><small><span class="mono">${account.uin}</span> · ${account.inventory_count} 种物品</small></span>
      <span class="level">Lv.${account.degree}</span>
    </button>`;
  }).join("") : `<p class="sidebar-hint">${state.accounts.length ? "没有匹配的本地账号。" : "还没有本地账号，点击“新建”创建一个。"}</p>`;
}

function confirmDiscardProfile() {
  return !state.profileDirty || confirm("人物资料有未保存的修改，确定放弃这些修改吗？");
}

// Internal helpers run inside runAccountOperation; only public actions acquire
// the lock, so create/delete can reload without nesting an account action.
async function loadAccount(uin) {
  if (!confirmDiscardProfile()) return false;
  const account = await api(`/gm/api/accounts/${uin}`);
  const accountChanged = state.account?.uin !== uin;
  // Publish the account and its selected identity together after loading.
  state.selectedUIN = uin;
  state.account = account;
  if (accountChanged) {
    state.inventoryPage = 1;
    state.inventoryKind = "";
    state.inventoryQuery = "";
    $("inventory-search").value = "";
  }
  $("empty-state").classList.add("hidden");
  $("account-workspace").classList.remove("hidden");
  renderAccounts();
  renderAccountHeader();
  renderProfile();
  renderInventory();
  renderPets();
  renderItemGrid();
  return true;
}

const selectAccount = accountAction(loadAccount);

function showEmpty() {
  state.selectedUIN = 0;
  state.account = null;
  setProfileDirty(false);
  $("empty-state").classList.remove("hidden");
  $("account-workspace").classList.add("hidden");
}

function winRateText(wins, losses, draws) {
  const total = wins + losses + draws;
  return total > 0 ? `${(wins / total * 100).toFixed(1)}%` : "—";
}

function renderAccountHeader() {
  const { uin, profile } = state.account;
  const game = profile.game_info;
  const rank = competitiveRankForPoints(game.points);
  const adventure = adventureRankForPoints(game.extended_points);
  const identity = state.progression?.identities.find((entry) => entry.value === profile.identity);
  const avatar = $("hero-avatar");
  avatar.style.setProperty("--hue", avatarHue(uin));
  avatar.textContent = avatarInitial(profile.nickname);
  $("profile-title").textContent = profile.nickname || "未命名";
  $("hero-meta").innerHTML = [
    `<span class="mono">QQ ${uin}</span>`,
    `<span class="mono">PlayerID ${profile.player_id}</span>`,
    `<span>${profile.gender === 1 ? "男性" : "女性"}</span>`,
    profile.identity ? `<span class="badge vip">${escapeHTML(identity?.label || `身份 ${profile.identity}`)}</span>` : "",
    profile.tutorial_completed ? "" : `<span class="badge warn">未完成新手教程</span>`,
  ].join("");
  const stats = [
    ["竞技等级", `Lv.${rank?.degree ?? game.degree}`, `${rank?.title || ""} · ${game.points.toLocaleString()} 积分`],
    ["探险等级", `Lv.${adventure?.level ?? "—"}`, `${game.extended_points.toLocaleString()} 经验`],
    ["QQ糖币", game.money.toLocaleString(), ""],
    ["战绩 胜/负/平", `${game.wins}/${game.losses}/${game.draws}`, `胜率 ${winRateText(game.wins, game.losses, game.draws)}`],
  ];
  $("hero-stats").innerHTML = stats.map(([label, value, note]) =>
    `<div class="stat"><dt>${label}</dt><dd title="${escapeHTML(value)}">${escapeHTML(value)}</dd>${note ? `<span>${escapeHTML(note)}</span>` : ""}</div>`).join("");
}

function renderProfile() {
  const { uin, profile } = state.account;
  const game = profile.game_info;
  setValue("profile-uin", uin); setValue("profile-player-id", profile.player_id); setValue("profile-nickname", profile.nickname);
  setValue("profile-gender", profile.gender); setValue("profile-degree", game.degree);
  setValue("profile-points", game.points); setValue("profile-adventure-points", game.extended_points); setValue("profile-money", game.money);
  setValue("profile-identity", profile.identity); setValue("profile-wins", game.wins); setValue("profile-losses", game.losses); setValue("profile-draws", game.draws);
  setValue("profile-password", "");
  $("profile-tutorial").checked = Boolean(profile.tutorial_completed);
  deriveCompetitiveRank();
  if (game.degree !== numberValue("profile-degree")) {
    $("profile-degree-detail").textContent += ` · 存档字段 Lv.${game.degree} 与积分不一致，保存后将统一`;
  }
  deriveAdventureRank();
  updateWinRate();
  setProfileDirty(false);
}

function updateWinRate() {
  $("profile-winrate").textContent = `胜率 ${winRateText(numberValue("profile-wins"), numberValue("profile-losses"), numberValue("profile-draws"))}`;
}

function setProfileDirty(dirty) {
  state.profileDirty = dirty;
  $("profile-dirty-dot").classList.toggle("hidden", !dirty);
  $("profile-savebar").classList.toggle("dirty", dirty);
  $("profile-dirty").textContent = dirty ? "有未保存的修改" : "资料已同步 · 修改保存后需重新登录客户端生效";
  $("reset-profile").disabled = !dirty;
}

function inventoryMetadata() {
  return new Map((state.account.inventory_items || []).map((item) => [item.id, item]));
}

function renderInventory() {
  const inventory = state.account.profile.inventory || [];
  const metadataByID = inventoryMetadata();
  const kinds = [...new Set(inventory.map((item) => metadataByID.get(item.id)?.kind || "unknown"))]
    .sort((left, right) => kindLabel(left).localeCompare(kindLabel(right), "zh-CN"));
  if (state.inventoryKind && !kinds.includes(state.inventoryKind)) state.inventoryKind = "";
  $("inventory-kind").innerHTML = `<option value="">全部分类</option>${kinds.map((kind) => `<option value="${escapeHTML(kind)}">${escapeHTML(kindLabel(kind))}</option>`).join("")}`;
  $("inventory-kind").value = state.inventoryKind;
  const query = state.inventoryQuery.trim().toLowerCase();
  const filtered = inventory.filter((item) => {
    const metadata = metadataByID.get(item.id);
    if (state.inventoryKind && (metadata?.kind || "unknown") !== state.inventoryKind) return false;
    return !query || `${item.id} ${metadata?.name || ""}`.toLowerCase().includes(query);
  });
  const pageCount = Math.max(1, Math.ceil(filtered.length / state.inventoryPageSize));
  state.inventoryPage = Math.min(Math.max(1, state.inventoryPage), pageCount);
  const start = (state.inventoryPage - 1) * state.inventoryPageSize;
  const visible = filtered.slice(start, start + state.inventoryPageSize);
  updateTabCounts();
  $("inventory-result").textContent = filtered.length === inventory.length ? `共 ${inventory.length} 种物品` : `筛选出 ${filtered.length} / ${inventory.length} 种物品`;
  $("inventory-page").textContent = `第 ${state.inventoryPage} / ${pageCount} 页`;
  $("inventory-prev").disabled = state.inventoryPage <= 1;
  $("inventory-next").disabled = state.inventoryPage >= pageCount;
  $("inventory-list").innerHTML = visible.length ? visible.map((item) => {
    const metadata = metadataByID.get(item.id);
    const name = metadata?.name || `物品 ${item.id}`;
    const equipped = (state.account.loadouts || []).find((entry) => entry.item_id === item.id);
    const imageRole = equipped?.role_id || selectedRole();
    return `
    <div class="inventory-row" data-motion-host data-item-id="${item.id}" data-item-name="${escapeHTML(name)}" data-quantity="${item.quantity}">
      <span class="slot"><img class="item-icon" tabindex="0" decoding="async" data-item-image="${item.id}" data-image-role="${imageRole}" data-image-appearance="${Boolean(equipped)}" src="${itemImageURL(item.id, imageRole, Boolean(equipped), false)}" alt="${escapeHTML(name)}"></span>
      <div class="item-copy">
        <div class="item-title"><strong title="${escapeHTML(name)}">${escapeHTML(name)}</strong>${equipped ? `<span class="badge info" title="${escapeHTML(loadoutText(item.id))}">已装备</span>` : ""}</div>
        <small class="item-meta"><span class="mono">ID ${item.id}</span> · ${escapeHTML(kindLabel(metadata?.kind || "unknown"))}${equipped ? ` · ${escapeHTML(loadoutText(item.id))}` : ""}</small>
        ${metadata?.description ? `<span class="inventory-item-description">${escapeHTML(metadata.description)}</span>` : ""}
      </div>
      <div class="quantity-editor">
        <span class="stepper"><button type="button" data-step="-1" aria-label="减少">−</button><input type="number" min="1" max="${maxQuantity}" value="${item.quantity}" aria-label="数量"><button type="button" data-step="1" aria-label="增加">＋</button></span>
        <button type="button" class="secondary compact save-item" disabled>保存</button>
      </div>
      <button type="button" class="ghost danger compact remove-item">移除</button>
    </div>`;
  }).join("") : `<p class="empty-line">${inventory.length ? "没有符合当前筛选条件的物品。" : "背包为空。点击上方“添加物品”从原版物品目录添加。"}</p>`;
  bindItemImageMotion();
}

function loadoutText(itemID) {
  const matches = (state.account.loadouts || []).filter((entry) => entry.item_id === itemID);
  return matches.map((entry) => `角色${entry.role_id}（${roleDisplayName(entry.role_id)}）/${slotDisplayName(entry.slot)}`).join("、");
}

function roleDisplayName(roleID) {
  // IDs and short resource names are taken from uiRoom.pyc RoleIDList /
  // RoleNameList. Keep unknown future roles readable without guessing.
  return ({ 1:"小悟空", 2:"泰坦", 3:"小倩", 4:"春丽", 5:"波波利", 6:"火影", 7:"玛丽亚", 8:"海王子", 9:"毛毛", 13:"可乐", 14:"哪吒", 15:"乌拉拉", 16:"丫丫", 22:"阿莎", 23:"随机角色" })[roleID] || "未知角色";
}

function slotDisplayName(slot) {
  return ({ cap:"帽子", hair:"头发", eye:"眼部", face:"面部", mouth:"嘴部", cloth:"服装", cloth_adornment:"服装饰品", front_pack:"前挂饰", back_pack:"背饰", ear:"耳饰", bubble:"糖泡", footprint:"脚印", background:"背景", frame:"资料边框", enter:"入场效果", namecard:"名片", namecard_bound:"名片边框" })[slot] || slot;
}

function updateTabCounts() {
  $("inventory-count").textContent = (state.account?.profile.inventory || []).length;
  $("pet-count").textContent = (state.account?.pets || []).length;
}

// Keep response ownership explicit even though account actions are serialized.
// A response must belong to both the selection and the displayed account.
function applyAccountResponse(uin, account) {
  const summary = state.accounts.find((entry) => entry.uin === uin);
  if (summary) summary.inventory_count = (account.profile.inventory || []).length;
  const current = state.selectedUIN === uin && state.account?.uin === uin;
  if (current) state.account = account;
  renderAccounts();
  updateTabCounts();
  return current;
}

function accountSuffix(uin, current) {
  return current ? "" : `（账号 ${uin}）`;
}

const saveProfile = accountAction(async () => {
  if (!state.account || !$("profile-form").reportValidity()) return;
  const uin = state.account.uin;
  const payload = {
    nickname: $("profile-nickname").value.trim(), gender: numberValue("profile-gender"),
    degree: numberValue("profile-degree"), points: numberValue("profile-points"), adventure_points: numberValue("profile-adventure-points"),
    money: numberValue("profile-money"), identity: numberValue("profile-identity"), wins: numberValue("profile-wins"),
    losses: numberValue("profile-losses"), draws: numberValue("profile-draws"), tutorial_completed: $("profile-tutorial").checked
  };
  const password = $("profile-password").value;
  let account = await api(`/gm/api/accounts/${uin}`, { method: "PUT", body: JSON.stringify(payload) });
  if (password) account = await api(`/gm/api/accounts/${uin}/password`, { method: "PUT", body: JSON.stringify({ password }) });
  const current = applyAccountResponse(uin, account);
  if (current) {
    renderAccountHeader();
    renderProfile();
  }
  toast(`人物资料已保存${accountSuffix(uin, current)}；重新登录客户端后生效。`);
  // The sidebar shows the server-derived rank, so re-read the summaries.
  await refreshAccountList();
});

function inventoryRowInput(row) {
  return row.querySelector(".stepper input");
}

function markInventoryRowChanged(row) {
  const value = Number(inventoryRowInput(row).value);
  const changed = value !== Number(row.dataset.quantity);
  row.classList.toggle("changed", changed);
  row.querySelector(".save-item").disabled = !changed;
}

const updateInventory = accountAction(async (itemID, quantity) => {
  if (!Number.isInteger(quantity) || quantity < 1 || quantity > maxQuantity) return toast("道具数量必须在 1–999 之间；删除请使用“移除”按钮。", true);
  const uin = state.account.uin;
  const name = itemName(itemID);
  const account = await api(`/gm/api/accounts/${uin}/inventory/${itemID}`, { method: "PUT", body: JSON.stringify({ quantity }) });
  const current = applyAccountResponse(uin, account);
  if (current) {
    renderInventory();
    renderItemGrid();
  }
  toast(`已将 ${name} 的数量改为 ${quantity}${accountSuffix(uin, current)}。`);
});

const removeInventory = accountAction(async (itemID) => {
  const uin = state.account.uin;
  const name = itemName(itemID);
  if (!confirm(`确定从背包移除 ${name}（ID ${itemID}）？相关角色装备槽也会清除。`)) return;
  const account = await api(`/gm/api/accounts/${uin}/inventory/${itemID}`, { method: "DELETE" });
  const current = applyAccountResponse(uin, account);
  if (current) {
    renderInventory();
    renderItemGrid();
  }
  toast(`已移除 ${name}${accountSuffix(uin, current)}。`);
});

async function loadPets() {
  const data = await api(`/gm/api/pets?role=${selectedRole()}&limit=100`);
  state.petCatalog = data.pets || [];
  state.petByType.clear();
  state.petCatalog.forEach((pet) => {
    if (pet.pet_type_id) state.petByType.set(pet.pet_type_id, pet);
  });
  renderOwnedPets();
  renderPetCatalog();
}

function renderOwnedPets() {
  const pets = state.account?.pets || [];
  updateTabCounts();
  $("owned-pet-list").innerHTML = pets.length ? pets.map((pet) => {
    const metadata = state.petByType.get(pet.pet_type_id);
    const carried = pet.state === 1;
    return `
    <article class="owned-pet" data-motion-host data-pet-id="${pet.pet_id}">
      <span class="slot">${petImageMarkup(metadata, pet.pet_type_id)}</span>
      <div class="owned-pet-copy">
        <div class="item-title"><strong>${escapeHTML(metadata?.name || pet.name || `宠物 ${pet.pet_type_id}`)}</strong><span class="badge${carried ? " ok" : ""}">${carried ? "携带中" : "未携带"}</span></div>
        <small><span class="mono">Lv.${pet.level}</span> · 实例 <span class="mono">${pet.pet_id}</span> · 实体类型 <span class="mono">${pet.pet_type_id}</span>${metadata?.card_item_id ? ` · 宠物卡 <span class="mono">${metadata.card_item_id}</span>` : ""}</small>
      </div>
      <button type="button" class="ghost danger compact remove-pet">移除</button>
    </article>`;
  }).join("") : `<p class="empty-line">尚未拥有宠物，可在下方图鉴中生成。</p>`;
  bindItemImageMotion();
}

function renderPetCatalog() {
  const ownedTypes = new Set((state.account?.pets || []).map((pet) => pet.pet_type_id));
  const query = $("pet-search").value.trim().toLowerCase();
  const filtered = state.petCatalog.filter((pet) => !query || `${pet.name} ${pet.type_name || ""} ${pet.description || ""} ${pet.pet_type_id || ""} ${pet.card_item_id || ""} ${pet.card_resource_id || ""} ${pet.level_4_resource_id || ""} ${pet.level_7_resource_id || ""}`.toLowerCase().includes(query));
  $("pet-total").textContent = `找到 ${filtered.length} / ${state.petCatalog.length} 种 · ${state.petCatalog.filter((pet) => pet.card_item_id).length} 种已关联原版宠物卡`;
  $("pet-grid").innerHTML = filtered.length ? filtered.map((pet) => {
    const owned = pet.pet_type_id && ownedTypes.has(pet.pet_type_id);
    const complete = pet.resource_status === "complete";
    const status = complete ? "1/4/7级模型完整" : pet.resource_status === "starter-only" ? "仅初始通用模型；4/7级外观缺失" : "模型资源缺失";
    const modelText = pet.level_7_resource_id ? `${pet.level_4_resource_id}/${pet.level_7_resource_id}` : `${pet.level_4_resource_id || "—"}`;
    const disabled = !pet.assignable || owned;
    const identity = pet.card_item_id ? `宠物卡 ${pet.card_item_id} / 资源 ${pet.card_resource_id} → 实体类型 ${pet.pet_type_id}` : pet.pet_type_id ? `实体类型 ${pet.pet_type_id} · 当前物品表无对应宠物卡` : "尚无 PetTypeID";
    return `<article class="pet-card${pet.assignable ? "" : " unmapped"}" data-motion-host title="${escapeHTML(pet.description || pet.reason || "PetCfg.ini 原始类型")}">
      <span class="slot">${petImageMarkup(pet, pet.pet_type_id || modelText)}</span>
      <div class="pet-card-copy">
        <strong>${escapeHTML(pet.name)}</strong>
        <small title="${escapeHTML(identity)}">${escapeHTML(identity)} · 模型 ${escapeHTML(modelText)}</small>
        <div class="pet-card-foot">
          <span class="badge ${complete ? "ok" : "warn"}" title="${escapeHTML(status)}">${complete ? "模型完整" : pet.resource_status === "starter-only" ? "仅初始模型" : "模型缺失"}</span>
          <button type="button" class="${disabled ? "secondary" : "soft"} compact grant-pet" data-type-id="${pet.pet_type_id || 0}" data-name="${escapeHTML(pet.name)}" ${disabled ? "disabled" : ""}>${owned ? "已拥有" : pet.assignable ? "生成宠物" : "等待类型映射"}</button>
        </div>
      </div>
    </article>`;
  }).join("") : `<p class="empty-line">没有匹配的宠物。</p>`;
  bindItemImageMotion();
}

function petImageMarkup(pet, fallbackLabel) {
  if (pet?.card_item_id && pet.has_image) {
    return `<img class="item-icon" loading="lazy" decoding="async" data-item-image="${pet.card_item_id}" data-image-role="${selectedRole()}" data-image-appearance="false" src="${itemImageURL(pet.card_item_id, selectedRole(), false, false)}" alt="${escapeHTML(pet.name)}">`;
  }
  if (pet?.image_url && pet.has_image) {
    return `<img class="item-icon" loading="lazy" decoding="async" src="${escapeHTML(pet.image_url)}" alt="${escapeHTML(pet.name)}">`;
  }
  return `<span class="pet-mark">${escapeHTML(fallbackLabel)}</span>`;
}

function renderPets() {
  renderOwnedPets();
  renderPetCatalog();
}

const grantPet = accountAction(async (petTypeID, name) => {
  if (!state.account) return toast("请先选择账号。", true);
  const uin = state.account.uin;
  const account = await api(`/gm/api/accounts/${uin}/pets/${petTypeID}`, { method: "PUT" });
  if (applyAccountResponse(uin, account)) renderPets();
  toast(`已给 ${account.profile.nickname} 添加宠物 ${name}。重新登录后生效。`);
});

const removePet = accountAction(async (petID) => {
  const uin = state.account.uin;
  if (!confirm(`确定移除宠物实例 ${petID}？`)) return;
  const account = await api(`/gm/api/accounts/${uin}/pet-instances/${petID}`, { method: "DELETE" });
  const current = applyAccountResponse(uin, account);
  if (current) renderPets();
  toast(`已移除宠物实例 ${petID}${accountSuffix(uin, current)}。`);
});

const deleteAccount = accountAction(async () => {
  const account = state.account;
  if (!account || !confirm(`确定删除本地账号存档 ${account.profile.nickname}（${account.uin}）及其背包？\n\n在线账号不能删除，请先让对应客户端正常退出。此操作不会在普通服务端重启时自动重建账号。`)) return;
  await api(`/gm/api/accounts/${account.uin}`, { method: "DELETE" });
  toast(`已删除账号 ${account.uin}。`);
  showEmpty();
  await loadAccounts();
});

function openCreateDialog() {
  if (state.accountBusy) return;
  const highest = state.accounts.reduce((max, account) => Math.max(max, account.uin), 0);
  if (highest) setValue("create-uin", highest + 1);
  $("create-dialog").showModal();
  $("create-nickname").focus();
}

const createAccount = accountAction(async () => {
  if (!$("create-form").reportValidity()) return;
  const payload = { uin: numberValue("create-uin"), nickname: $("create-nickname").value.trim(), gender: numberValue("create-gender"), password: $("create-password").value };
  const account = await api("/gm/api/accounts", { method: "POST", body: JSON.stringify(payload) });
  $("create-dialog").close();
  setValue("create-nickname", "");
  toast(`账号 ${account.uin} 已创建。`);
  await loadAccounts(account.uin);
});

function bindPasswordToggle(buttonID, inputID) {
  $(buttonID).addEventListener("click", () => {
    const input = $(inputID);
    const visible = input.type === "text";
    input.type = visible ? "password" : "text";
    $(buttonID).textContent = visible ? "显示" : "隐藏";
  });
}

let itemSearchTimer = 0;
async function loadItems(resetPage = true) {
  if (resetPage) state.itemPage = 1;
  if (state.itemAbort) state.itemAbort.abort();
  const controller = new AbortController();
  state.itemAbort = controller;
  setItemPaginationDisabled(true);
  $("item-grid").classList.add("is-loading");
  const query = $("item-search").value.trim();
  const kind = $("item-kind").value;
  const requestedPage = state.itemPage;
  const offset = (requestedPage - 1) * state.itemPageSize;
  try {
    const images = $("item-images-only").checked ? "local" : "";
    const data = await api(`/gm/api/items?q=${encodeURIComponent(query)}&kind=${encodeURIComponent(kind)}&images=${images}&role=${selectedRole()}&offset=${offset}&limit=${state.itemPageSize}`, { signal: controller.signal });
    if (state.itemAbort !== controller) return;
    state.itemTotal = data.total;
    state.itemRows = data.items || [];
    const pageCount = itemPageCount();
    state.itemPage = Math.min(requestedPage, pageCount);
    if ($("item-kind").options.length === 1) data.kinds.forEach((value) => $("item-kind").add(new Option(kindLabel(value), value)));
    const firstVisible = state.itemRows.length ? offset + 1 : 0;
    const lastVisible = offset + state.itemRows.length;
    $("item-total").textContent = `找到 ${data.total} 项 · 当前显示 ${firstVisible}–${lastVisible}`;
    renderItemGrid();
    updateItemPagination();
  } catch (error) {
    if (error.name !== "AbortError") throw error;
  } finally {
    if (state.itemAbort === controller) {
      state.itemAbort = null;
      $("item-grid").classList.remove("is-loading");
      updateItemPagination();
    }
  }
}

function itemPageCount() {
  return Math.max(1, Math.ceil(state.itemTotal / state.itemPageSize));
}

function setItemPaginationDisabled(disabled) {
  document.querySelectorAll("[data-item-page]").forEach((button) => { button.disabled = disabled; });
}

function updateItemPagination() {
  const pageCount = itemPageCount();
  const label = `第 ${state.itemPage} / ${pageCount} 页`;
  $("item-page").textContent = label;
  $("item-page-bottom").textContent = label;
  document.querySelectorAll("[data-item-page]").forEach((button) => {
    const backward = button.dataset.itemPage === "first" || button.dataset.itemPage === "prev";
    button.disabled = backward ? state.itemPage <= 1 : state.itemPage >= pageCount;
  });
}

function changeItemPage(page) {
  const nextPage = Math.min(Math.max(1, page), itemPageCount());
  if (nextPage === state.itemPage) return false;
  state.itemPage = nextPage;
  loadItems(false).catch(reportError);
  return true;
}

function renderItemGrid() {
  const owned = new Map((state.account?.profile.inventory || []).map((entry) => [entry.id, entry.quantity]));
  $("item-grid").innerHTML = state.itemRows.length
    ? state.itemRows.map((item) => itemCard(item, owned.get(item.id) || 0)).join("")
    : `<p class="empty-line">没有符合当前条件的物品。</p>`;
  bindItemImageMotion();
}

function itemCard(item, ownedQuantity) {
  const appearance = Boolean(item.has_appearance);
  const sourceKey = appearance ? item.appearance_source : item.image_source;
  const catalogSource = imageSourceLabel(item.image_source);
  const description = String(item.description || "").trim();
  const full = ownedQuantity >= maxQuantity;
  return `<article class="item-card${description ? " has-description" : ""}" data-motion-host data-item-id="${item.id}" data-item-name="${escapeHTML(item.name)}"${description ? ` aria-describedby="item-description-${item.id}"` : ""}>
    <div class="item-media">
      <img class="item-icon${item.has_image ? "" : " missing"}" tabindex="0" loading="lazy" decoding="async" data-item-image="${item.id}" data-image-role="${selectedRole()}" data-image-appearance="${appearance}" src="${itemImageURL(item.id, selectedRole(), appearance, false)}" alt="${escapeHTML(item.name)}">
      ${ownedQuantity ? `<span class="owned-badge" title="当前账号背包已有 ${ownedQuantity} 个">已有 ×${ownedQuantity}</span>` : ""}
      ${item.scene_id_collision ? `<span class="namespace-note" title="客户端对局场景工厂也识别相同数字 ID；两者资源命名空间彼此独立">同号场景元素</span>` : ""}
    </div>
    <strong class="item-name" title="${escapeHTML(item.name)}">${escapeHTML(item.name)}</strong>
    <small class="item-meta" title="${escapeHTML(kindLabel(item.kind))}${item.slot ? ` · ${escapeHTML(slotDisplayName(item.slot))}` : ""}"><span class="mono">ID ${item.id}</span> · ${escapeHTML(kindLabel(item.kind))}${item.slot ? ` · ${escapeHTML(slotDisplayName(item.slot))}` : ""}</small>
    <div class="item-foot">
      <code class="resource-key" title="账号库存资源键；不与同号对局元素混用">${escapeHTML(item.resource_key)}</code>
      <span class="image-source${item.has_image ? "" : " missing"}" title="目录图标资源：${escapeHTML(catalogSource)}${appearance ? `；预览：${escapeHTML(imageSourceLabel(sourceKey))}` : ""}">${escapeHTML(imageSourceShortLabel(item.has_image ? sourceKey : ""))}</span>
    </div>
    <button type="button" class="soft compact grant-item" data-id="${item.id}" data-name="${escapeHTML(item.name)}"${full ? ` disabled title="背包中已达 999 上限"` : ""}>${full ? "已达上限" : "加入背包"}</button>
    ${description ? `<div class="item-description-tooltip" id="item-description-${item.id}" role="tooltip">${escapeHTML(description)}</div>` : ""}
  </article>`;
}

function imageSourceLabel(value) {
  return ({ "item-icon":"原版物品图标", "category-icon":"原版分类图标", "appearance-layer":"当前角色原版装扮图层", "canonical-alias":"原版规范物品映射", "explicit-alias":"原版显式资源映射" })[value] || "当前客户端未含对应图片";
}

function imageSourceShortLabel(value) {
  return ({ "item-icon":"原版图标", "category-icon":"分类图标", "appearance-layer":"装扮图层", "canonical-alias":"规范映射", "explicit-alias":"显式映射" })[value] || "无本地图";
}

function ownedQuantity(itemID) {
  return (state.account?.profile.inventory || []).find((entry) => entry.id === itemID)?.quantity || 0;
}

function grantItem(itemID, name) {
  if (state.accountBusy) return;
  if (!state.account) return toast("请先选择账号。", true);
  const existing = ownedQuantity(itemID);
  state.pendingGrantItem = { uin: state.account.uin, itemID, name, existing };
  $("grant-item-name").textContent = `${name}（ID ${itemID}）`;
  $("grant-item-owned").textContent = existing ? `当前背包已有 ${existing} 个` : "当前背包中没有此物品";
  $("grant-item-image").src = itemImageURL(itemID, selectedRole(), false, false);
  $("grant-item-image").alt = name;
  $("grant-item-quantity").max = Math.max(1, maxQuantity - existing);
  setValue("grant-item-quantity", 1);
  updateGrantResult();
  $("grant-item-dialog").showModal();
  $("grant-item-quantity").focus();
  $("grant-item-quantity").select();
}

function updateGrantResult() {
  const pending = state.pendingGrantItem;
  if (!pending) return;
  const quantity = numberValue("grant-item-quantity");
  const room = maxQuantity - pending.existing;
  const node = $("grant-item-result");
  let error = "";
  if (room <= 0) error = "背包中此物品已达 999 上限，无法继续添加。";
  else if (!Number.isInteger(quantity) || quantity < 1) error = "添加数量至少为 1。";
  else if (quantity > room) error = `最多还能添加 ${room} 个（上限 999）。`;
  node.textContent = error || `添加后背包共 ${pending.existing + quantity} 个`;
  node.classList.toggle("error", Boolean(error));
  $("confirm-grant-item").disabled = Boolean(error);
}

function setGrantQuantity(value) {
  const room = Math.max(1, maxQuantity - (state.pendingGrantItem?.existing || 0));
  setValue("grant-item-quantity", Math.min(Math.max(1, value), room));
  updateGrantResult();
}

const confirmGrantItem = accountAction(async () => {
  const pending = state.pendingGrantItem;
  if (!pending) return;
  const quantity = numberValue("grant-item-quantity");
  if (!Number.isInteger(quantity) || quantity < 1 || quantity > maxQuantity) return toast("添加数量必须在 1–999 之间。", true);
  // Only trust the live inventory while it still belongs to the dialog's account.
  const existing = state.account?.uin === pending.uin ? ownedQuantity(pending.itemID) : pending.existing;
  const next = existing + quantity;
  if (next > maxQuantity) return toast(`当前已有 ${existing} 个，添加后不能超过 999。`, true);
  const account = await api(`/gm/api/accounts/${pending.uin}/inventory/${pending.itemID}`, { method: "PUT", body: JSON.stringify({ quantity: next }) });
  if (state.pendingGrantItem === pending) {
    $("grant-item-dialog").close();
    state.pendingGrantItem = null;
  }
  if (applyAccountResponse(pending.uin, account)) {
    renderInventory();
    renderItemGrid();
  }
  toast(`已给 ${account.profile.nickname} 添加 ${pending.name} × ${quantity}。`);
});

function switchTab(tab, focus = false) {
  state.tab = tab;
  document.querySelectorAll("[data-tab]").forEach((button) => {
    const active = button.dataset.tab === tab;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
    button.tabIndex = active ? 0 : -1;
    if (active && focus) button.focus();
  });
  document.querySelectorAll("[data-panel]").forEach((panel) => panel.classList.toggle("hidden", panel.dataset.panel !== tab));
  try { localStorage.setItem("gm-tab", tab); } catch (error) { /* storage unavailable */ }
}

function kindLabel(value) {
  return ({ "pet-food":"宠物粮食", "pet-card":"宠物卡", "profile-decoration":"资料装饰", "avatar-cosmetic":"角色装扮", "pet-skill-book":"宠物技能书", "craft-recipe":"合成书", "material":"材料", "forge-gem":"宝石", "inventory-consumable":"消耗品", "account-item":"账号物品", "unknown":"未分类物品" })[value] || value;
}
function escapeHTML(value) { const node = document.createElement("span"); node.textContent = String(value ?? ""); return node.innerHTML; }
function setValue(id, value) { $(id).value = value ?? ""; }
function numberValue(id) { return Number($(id).value); }

// Only a press that both starts and ends on the backdrop closes the dialog, so
// dragging a text selection out of an input does not dismiss it.
function closeOnBackdrop(dialog, onClose) {
  let pressedBackdrop = false;
  dialog.addEventListener("pointerdown", (event) => { pressedBackdrop = event.target === dialog; });
  dialog.addEventListener("click", (event) => {
    if (state.accountBusy || event.target !== dialog || !pressedBackdrop) return;
    dialog.close();
    onClose?.();
  });
}

function bindEvents() {
  $("account-search").addEventListener("input", renderAccounts);
  $("account-list").addEventListener("click", (event) => {
    const card = event.target.closest(".account-card");
    if (card) selectAccount(Number(card.dataset.uin)).catch(reportError);
  });
  $("new-account").addEventListener("click", openCreateDialog);
  $("empty-create").addEventListener("click", openCreateDialog);
  $("cancel-create").addEventListener("click", () => $("create-dialog").close());
  closeOnBackdrop($("create-dialog"));
  bindPasswordToggle("toggle-create-password", "create-password");
  bindPasswordToggle("toggle-profile-password", "profile-password");
  $("create-form").addEventListener("submit", (event) => { event.preventDefault(); createAccount().catch(reportError); });
  [$("create-dialog"), $("grant-item-dialog")].forEach((dialog) => dialog.addEventListener("cancel", (event) => {
    if (state.accountBusy) event.preventDefault();
  }));

  document.querySelectorAll("[data-tab]").forEach((button) => button.addEventListener("click", () => switchTab(button.dataset.tab)));
  document.querySelector(".tabs").addEventListener("keydown", (event) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    const tabs = [...document.querySelectorAll("[data-tab]")].map((button) => button.dataset.tab);
    const index = tabs.indexOf(state.tab) + (event.key === "ArrowRight" ? 1 : -1);
    switchTab(tabs[(index + tabs.length) % tabs.length], true);
    event.preventDefault();
  });

  $("profile-form").addEventListener("submit", (event) => { event.preventDefault(); saveProfile().catch(reportError); });
  $("profile-form").addEventListener("input", () => setProfileDirty(true));
  $("profile-form").addEventListener("change", () => setProfileDirty(true));
  $("reset-profile").addEventListener("click", () => { if (state.account) renderProfile(); });
  $("delete-account").addEventListener("click", () => deleteAccount().catch(reportError));
  $("refresh-account").addEventListener("click", () => selectAccount(state.selectedUIN).then((done) => { if (done) toast("已刷新账号数据。"); }).catch(reportError));
  $("profile-degree").addEventListener("change", selectCompetitiveRank);
  $("profile-points").addEventListener("input", deriveCompetitiveRank);
  $("profile-adventure-level").addEventListener("change", selectAdventureRank);
  $("profile-adventure-points").addEventListener("input", deriveAdventureRank);
  ["profile-wins", "profile-losses", "profile-draws"].forEach((id) => $(id).addEventListener("input", updateWinRate));
  window.addEventListener("beforeunload", (event) => {
    if (!state.profileDirty) return;
    event.preventDefault();
    event.returnValue = "";
  });

  $("inventory-search").addEventListener("input", () => { state.inventoryQuery = $("inventory-search").value; state.inventoryPage = 1; renderInventory(); });
  $("inventory-kind").addEventListener("change", () => { state.inventoryKind = $("inventory-kind").value; state.inventoryPage = 1; renderInventory(); });
  $("inventory-prev").addEventListener("click", () => { state.inventoryPage--; renderInventory(); });
  $("inventory-next").addEventListener("click", () => { state.inventoryPage++; renderInventory(); });
  $("goto-catalog").addEventListener("click", () => { switchTab("catalog"); $("item-search").focus(); });
  $("inventory-list").addEventListener("click", (event) => {
    const row = event.target.closest(".inventory-row");
    if (!row) return;
    const itemID = Number(row.dataset.itemId);
    const step = event.target.closest("[data-step]");
    if (step) {
      const input = inventoryRowInput(row);
      input.value = Math.min(Math.max(1, (Number(input.value) || 0) + Number(step.dataset.step)), maxQuantity);
      markInventoryRowChanged(row);
    } else if (event.target.closest(".save-item")) {
      updateInventory(itemID, Number(inventoryRowInput(row).value)).catch(reportError);
    } else if (event.target.closest(".remove-item")) {
      removeInventory(itemID).catch(reportError);
    }
  });
  $("inventory-list").addEventListener("input", (event) => {
    const row = event.target.closest(".inventory-row");
    if (row) markInventoryRowChanged(row);
  });
  $("inventory-list").addEventListener("keydown", (event) => {
    const row = event.target.closest(".inventory-row");
    if (event.key !== "Enter" || !row || !event.target.matches(".stepper input")) return;
    event.preventDefault();
    if (row.classList.contains("changed")) updateInventory(Number(row.dataset.itemId), Number(event.target.value)).catch(reportError);
  });

  $("pet-search").addEventListener("input", renderPetCatalog);
  $("owned-pet-list").addEventListener("click", (event) => {
    const button = event.target.closest(".remove-pet");
    if (button) removePet(Number(button.closest(".owned-pet").dataset.petId)).catch(reportError);
  });
  $("pet-grid").addEventListener("click", (event) => {
    const button = event.target.closest(".grant-pet:not(:disabled)");
    if (button) grantPet(Number(button.dataset.typeId), button.dataset.name).catch(reportError);
  });

  $("item-search").addEventListener("input", () => { clearTimeout(itemSearchTimer); itemSearchTimer = setTimeout(() => loadItems(true).catch(reportError), 250); });
  $("item-kind").addEventListener("change", () => loadItems(true).catch(reportError));
  $("item-images-only").addEventListener("change", () => loadItems(true).catch(reportError));
  document.querySelectorAll("[data-item-page]").forEach((button) => button.addEventListener("click", () => {
    const target = { first: 1, prev: state.itemPage - 1, next: state.itemPage + 1, last: itemPageCount() }[button.dataset.itemPage];
    if (changeItemPage(target) && button.hasAttribute("data-scroll-top")) $("catalog-panel").scrollIntoView({ block: "start" });
  }));
  $("item-grid").addEventListener("click", (event) => {
    const button = event.target.closest(".grant-item:not(:disabled)");
    if (button) grantItem(Number(button.dataset.id), button.dataset.name);
  });

  $("grant-item-form").addEventListener("submit", (event) => { event.preventDefault(); confirmGrantItem().catch(reportError); });
  $("grant-item-quantity").addEventListener("input", updateGrantResult);
  $("grant-item-form").addEventListener("click", (event) => {
    const step = event.target.closest("[data-grant-step]");
    const preset = event.target.closest("[data-grant-set]");
    if (step) setGrantQuantity((numberValue("grant-item-quantity") || 0) + Number(step.dataset.grantStep));
    else if (preset) setGrantQuantity(preset.dataset.grantSet === "max" ? maxQuantity : Number(preset.dataset.grantSet));
  });
  $("cancel-grant-item").addEventListener("click", () => { state.pendingGrantItem = null; $("grant-item-dialog").close(); });
  closeOnBackdrop($("grant-item-dialog"), () => { state.pendingGrantItem = null; });
}

function reportError(error) { console.error(error); toast(error.message || String(error), true); }

async function start() {
  bindEvents();
  let savedTab = "";
  try { savedTab = localStorage.getItem("gm-tab") || ""; } catch (error) { /* storage unavailable */ }
  if (document.querySelector(`[data-tab="${savedTab}"]`)) switchTab(savedTab);
  await runAccountOperation(async () => {
    await Promise.all([loadHealth(), loadProgression(), loadItems(true), loadPets()]);
    await loadAccounts();
  });
}
start().catch(reportError);
