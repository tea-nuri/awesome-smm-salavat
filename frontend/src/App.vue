<script setup>
import {
  ref,
  computed,
  onMounted,
  onUnmounted,
  watch,
  defineComponent,
  h,
} from "vue";
import {
  ArrowUpRight,
  ArrowRight,
  Plus,
  Sparkles,
  Pause,
  Play,
  LayoutDashboard,
  Layers,
  CalendarClock,
  Building2,
  Settings2,
  Bell,
  ChevronDown,
  Check,
  X,
  RotateCcw,
  Send,
  FileText,
  Clock3,
  ShieldCheck,
  ExternalLink,
  LoaderCircle,
  Image as ImageIcon,
  Menu,
  Upload,
  CircleHelp,
  ChevronRight,
  LogOut,
  Activity,
  Bot,
  Zap,
} from "lucide-vue-next";
const tg = window.Telegram?.WebApp;
tg?.ready();
tg?.expand();
try {
  tg?.setHeaderColor("#f3f6f6");
  tg?.setBackgroundColor("#f3f6f6");
} catch {}
const state = ref(null),
  current = ref(""),
  tab = ref("overview"),
  error = ref(""),
  busy = ref(false),
  loading = ref(true),
  localToken = ref(""),
  task = ref(""),
  selected = ref(null),
  revisionComment = ref(""),
  editing = ref(false),
  editBody = ref(""),
  toast = ref(""),
  companyModal = ref(false),
  companyMenu = ref(false);
const form = ref({ name: "", brief: "", tone: "", audience: "", channel: "" });
const schedule = ref({ request: "", next_run: "", every_seconds: 0 });
const taskPhotos = ref([]), revisionPhotos = ref([]), revisionScope = ref("all"), uploadingPhotos = ref(false);
const taskRatio = ref("1:1"), revisionRatio = ref("1:1");
const taskLogo = ref(""), revisionLogo = ref("");
const taskStyles = ref([]), revisionStyles = ref([]);
const branding = ref({ logo_asset_id: "", style_assets: [], style_notes: "", emoji_source: "references", emoji_pack: "" });
async function uploadBrandFiles(event, target) {
  const files = Array.from(event.target.files || []);
  event.target.value = "";
  const companyID = current.value, postID = selected.value?.id;
  const styles = target === "company-style" ? branding.value.style_assets : target === "task-style" ? taskStyles.value : revisionStyles.value;
  if (target.endsWith("style") && styles.length + files.length > 8) { notify("Можно добавить до 8 примеров стиля"); return; }
  uploadingPhotos.value = true;
  try {
    for (const file of files) {
      if (!["image/jpeg", "image/png"].includes(file.type) || file.size > 12 * 1024 * 1024) throw new Error("Выберите JPG или PNG до 12 МБ");
      const response = await fetch("/api/assets?company_id=" + encodeURIComponent(companyID), { method: "POST", body: file, headers: { ...headers(), "Content-Type": file.type, "X-File-Name": encodeURIComponent(file.name) } });
      const asset = await response.json();
      if (!response.ok) throw new Error(asset.error || "Не удалось загрузить файл");
      if (current.value !== companyID || (target.startsWith("revision") && selected.value?.id !== postID)) throw new Error("Открытая компания или материал изменились. Добавьте файл заново");
      if (target.endsWith("style")) styles.push(asset.id);
      else if (target === "company-logo") branding.value.logo_asset_id = asset.id;
      else if (target === "task-logo") taskLogo.value = asset.id;
      else revisionLogo.value = asset.id;
      if (target.startsWith("revision")) revisionScope.value = "image";
    }
  } catch (err) { notify(err.message); }
  finally { uploadingPhotos.value = false; }
}
const imageFormats = [
  { value: "1:1", label: "Квадрат", shape: "square" },
  { value: "4:5", label: "Вертикаль", shape: "portrait" },
  { value: "16:9", label: "Широкий", shape: "wide" },
];
function clearPhotos(target) {
  for (const photo of target.value) URL.revokeObjectURL(photo.preview);
  target.value = [];
}
function removePhoto(target, index) {
  URL.revokeObjectURL(target.value[index].preview);
  target.value.splice(index, 1);
}
function removeTaskPhoto(index) { removePhoto(taskPhotos, index); }
function removeRevisionPhoto(index) { removePhoto(revisionPhotos, index); }
async function addPhotos(event, revision = false) {
  const target = revision ? revisionPhotos : taskPhotos;
  const files = Array.from(event.target.files || []);
  event.target.value = "";
  if (target.value.length + files.length > 6) { notify("Можно приложить до 6 фотографий"); return; }
  const companyID = current.value;
  const postID = revision ? selected.value?.id : null;
  uploadingPhotos.value = true;
  try {
    for (const file of files) {
      if (!["image/jpeg", "image/png"].includes(file.type) || file.size > 12 * 1024 * 1024) throw new Error("Выберите JPG или PNG не больше 12 МБ");
      const response = await fetch("/api/assets?company_id=" + encodeURIComponent(companyID), {
        method: "POST", body: file,
        headers: { ...headers(), "Content-Type": file.type, "X-File-Name": encodeURIComponent(file.name) },
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || "Не удалось загрузить фото");
      if (current.value !== companyID) throw new Error("Компания изменилась. Добавьте фото в выбранную компанию заново");
      if (revision && selected.value?.id !== postID) throw new Error("Материал изменился. Откройте его и добавьте фото заново");
      target.value.push({ id: result.id, name: file.name, preview: URL.createObjectURL(file) });
      if (revision) revisionScope.value = "image";
    }
  } catch (err) { notify(err.message); }
  finally { uploadingPhotos.value = false; }
}
watch(current, () => { clearPhotos(taskPhotos); clearPhotos(revisionPhotos); taskLogo.value = ""; revisionLogo.value = ""; taskStyles.value = []; revisionStyles.value = []; });
watch(() => selected.value?.id, () => { clearPhotos(revisionPhotos); revisionScope.value = "all"; revisionLogo.value = ""; revisionStyles.value = []; revisionRatio.value = selected.value?.aspect_ratio || "1:1"; });
let poll, toastTimer;
const headers = () => ({
  "Content-Type": "application/json",
  "X-Telegram-Init-Data": tg?.initData || "",
  ...(localToken.value ? { "X-Local-Token": localToken.value } : {}),
});
async function api(path, data, method) {
  const r = await fetch("/api/" + path, {
    method: method || (data ? "POST" : "GET"),
    headers: headers(),
    ...(data ? { body: JSON.stringify(data) } : {}),
  });
  const result = await r.json();
  if (!r.ok) throw new Error(result.error || "Не удалось выполнить запрос");
  return result;
}
function notify(text) {
  toast.value = text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = ""), 4000);
}
async function refresh() {
  try {
    const data = await api("bootstrap");
    state.value = data;
    if (!current.value || !data.companies.some((c) => c.id === current.value)) {
      current.value = data.active_company || data.companies[0]?.id || "";
    }
    if (selected.value) {
      selected.value =
        data.posts.find((p) => p.id === selected.value.id) || null;
    }
  } catch (e) {
    error.value = e.message;
  } finally {
    loading.value = false;
  }
}
const company = computed(() =>
  state.value?.companies.find((c) => c.id === current.value),
);
const isSalavat = computed(() => company.value?.brand_profile === "salavatsteklo");
async function useSalavatTemplate() {
  const templates = await api("brand-templates");
  form.value = { ...form.value, ...templates.salavatsteklo };
}
const PostText = defineComponent({
  props: ["post"],
  setup(props) {
    return () => {
      const post = props.post;
      const lines = (post.body || post.request || "").split("\n");
      return h("div", { class: "post-full" }, lines.map((line, i) => {
        const quote = post.quote && line && post.quote.includes(line);
        const parts = [];
        let tail = line;
        for (const phrase of post.emphasis || []) {
          if (!phrase || !tail.includes(phrase)) continue;
          const at = tail.indexOf(phrase);
          parts.push(tail.slice(0, at), h("strong", phrase));
          tail = tail.slice(at + phrase.length);
        }
        parts.push(tail);
        return h(quote ? "blockquote" : "div", { class: { "post-hook": i === 0, "post-space": !line, "post-hashtag": line.startsWith("#") } }, parts);
      }));
    };
  },
});
const companyTasks = computed(
  () => state.value?.posts.filter((p) => p.company_id === current.value) || [],
);
const posts = computed(() => companyTasks.value.filter((p) =>
  !["answered", "generating", "working"].includes(p.status) &&
  !["answer", "clarification"].includes(p.kind)
));
const pending = computed(() =>
  posts.value.filter((p) => p.status === "review"),
);
const published = computed(() =>
  posts.value.filter((p) => p.status === "published"),
);
const working = computed(() =>
  companyTasks.value.filter((p) => ["working", "generating"].includes(p.status)),
);
const events = computed(() =>
  (state.value?.events.filter((e) => e.company_id === current.value) || [])
    .slice(-8)
    .reverse(),
);
const schedules = computed(
  () =>
    state.value?.schedules.filter((s) => s.company_id === current.value) || [],
);
const labels = {
  generating: "В очереди",
  working: "Готовится",
  review: "На согласовании",
  approved: "Согласовано",
  publishing: "Публикуется",
  published: "Опубликовано",
  rejected: "Отклонено",
  failed: "Ошибка",
  answered: "Ответ готов",
  uncertain: "Проверьте канал",
  cancelled: "Отменено",
};
const fmt = (date) =>
  new Date(date).toLocaleString("ru-RU", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
async function choose(c) {
  current.value = c.id;
  companyMenu.value = false;
  selected.value = null;
  await api("active", { company_id: c.id });
  setForm();
}
function setForm() {
  if (company.value) branding.value = { logo_asset_id: company.value.logo_asset_id || "", style_assets: [...(company.value.style_assets || [])], style_notes: company.value.style_notes || "", emoji_source: company.value.emoji_source || "references", emoji_pack: company.value.emoji_pack || "", emoji_title: company.value.emoji_title || "", emoji_items: company.value.emoji_items || [] };
  if (company.value)
    form.value = {
      ...company.value,
      members_text: (company.value.members || []).join(", "),
    };
}
watch(tab, (v) => {
  if (v === "settings") setForm();
});
async function run(fn) {
  busy.value = true;
  error.value = "";
  try {
    await fn();
  } catch (e) {
    error.value = e.message;
  } finally {
    busy.value = false;
  }
}
async function createTask() {
  if (!task.value.trim()) return;
  await run(async () => {
    await api("tasks", { company_id: current.value, request: task.value, attachments: taskPhotos.value.map(p => p.id), image_mode: "style", aspect_ratio: taskRatio.value, logo_asset_id: taskLogo.value, ...(taskStyles.value.length ? { style_assets: taskStyles.value } : {}) });
    clearPhotos(taskPhotos);
    taskLogo.value = ""; taskStyles.value = [];
    task.value = "";
    notify("Задача принята. Готовим материал через ChatGPT.");
    await refresh();
  });
}
async function action(p, kind, comment = "") {
  await run(async () => {
    await api(`posts/${p.id}/${kind}`, {
      revision: p.revision,
      comment,
      ...(kind === "edit" ? { title: p.title, body: editBody.value } : {}),
      ...(kind === "revise" ? {
        ...(revisionScope.value !== "text" && revisionPhotos.value.length ? { attachments: revisionPhotos.value.map(photo => photo.id) } : {}),
        ...(revisionScope.value !== "text" && revisionStyles.value.length ? { style_assets: revisionStyles.value } : {}),
        ...(revisionScope.value !== "text" && revisionLogo.value ? { logo_asset_id: revisionLogo.value } : {}),
        image_mode: "style", image_only: revisionScope.value === "image", text_only: revisionScope.value === "text",
        aspect_ratio: revisionScope.value === "text" ? (p.aspect_ratio || "1:1") : revisionRatio.value,
      } : {}),
    });
    revisionComment.value = "";
    clearPhotos(revisionPhotos); revisionLogo.value = ""; revisionStyles.value = [];
    editing.value = false;
    notify(
      kind === "approve"
        ? "Одобрено. Отправляем в канал."
        : kind === "reject"
          ? "Публикация отклонена."
          : kind === "revise"
            ? "Правки приняты. Готовим новую версию."
            : "Изменения сохранены.",
    );
    await refresh();
  });
}
async function saveCompany() {
  await run(async () => {
    await api("companies/" + current.value + "/branding", branding.value, "PUT");
    await api(
      "companies/" + current.value,
      {
        ...form.value,
        members: (form.value.members_text || "")
          .split(",")
          .map((s) => Number(s.trim()))
          .filter((n) => Number.isSafeInteger(n) && n > 0),
      },
      "PUT",
    );
    notify("Настройки и знания сохранены");
    await refresh();
  });
}
async function addCompany() {
  await run(async () => {
    const c = await api("companies", form.value);
    companyModal.value = false;
    await refresh();
    await choose(c);
    tab.value = "settings";
    notify("Компания создана");
  });
}
async function addSchedule() {
  await run(async () => {
    if (!schedule.value.next_run) throw new Error("Выберите время");
    await api("schedules", {
      company_id: current.value,
      ...schedule.value,
      next_run: new Date(schedule.value.next_run).toISOString(),
      every_seconds: Number(schedule.value.every_seconds),
    });
    schedule.value = { request: "", next_run: "", every_seconds: 0 };
    notify("Задача запланирована");
    await refresh();
  });
}
async function toggleSchedule(s, enabled = !s.enabled) {
  await run(async () => {
    const seconds = s.every_seconds || (s.every_minutes || 0) * 60;
    await api(
      "schedules/" + s.id,
      {
        enabled,
        every_seconds: seconds,
        next_run: new Date(
          Date.now() + Math.max(seconds, 30) * 1000,
        ).toISOString(),
      },
      "PUT",
    );
    await refresh();
    notify(enabled ? "Автоподготовка включена" : "Автоподготовка на паузе");
  });
}
async function pauseAllSchedules() {
  await run(async () => {
    for (const s of schedules.value.filter((s) => s.enabled)) {
      const seconds = s.every_seconds || (s.every_minutes || 0) * 60;
      await api(
        "schedules/" + s.id,
        {
          enabled: false,
          every_seconds: seconds,
          next_run: new Date(
            Date.now() + Math.max(seconds, 30) * 1000,
          ).toISOString(),
        },
        "PUT",
      );
    }
    await refresh();
    notify("Автоподготовка остановлена");
  });
}
async function removeSchedule(s) {
  await run(async () => {
    await api("schedules/" + s.id, null, "DELETE");
    await refresh();
    notify("Расписание удалено");
  });
}
async function readFile(e) {
  const file = e.target.files?.[0];
  if (!file) return;
  if (file.size > 50000) {
    error.value =
      "Для формы выберите текстовый файл до 50 КБ. PDF можно отправить боту.";
    return;
  }
  form.value.brief = (form.value.brief || "") + "\n\n" + (await file.text());
  notify("Текст добавлен. Сохраните настройки.");
}
const Creative = defineComponent({
  props: { name: String, asset: Boolean },
  setup(props) {
    const src = ref("");
    let old = "";
    const load = async () => {
      if (!props.name) return;
      try {
        const r = await fetch((props.asset ? "/api/assets/" : "/media/") + props.name, { headers: headers() });
        if (!r.ok) return;
        const b = await r.blob();
        if (old) URL.revokeObjectURL(old);
        old = URL.createObjectURL(b);
        src.value = old;
      } catch {}
    };
    onMounted(load);
    watch(() => props.name, load);
    onUnmounted(() => {
      if (old) URL.revokeObjectURL(old);
    });
    return () =>
      src.value
        ? h("img", {
            src: src.value,
            alt: props.asset ? "Файл бренда" : "Визуал публикации",
            class: "creative-img",
          })
        : h("div", { class: "creative-loading" }, [h(ImageIcon, { size: 28 })]);
  },
});
onMounted(async () => {
  try {
    if (
      !tg?.initData &&
      ["localhost", "127.0.0.1"].includes(location.hostname)
    ) {
      const r = await fetch("/api/local-session");
      if (r.ok) localToken.value = (await r.json()).token;
    }
    await refresh();
    poll = setInterval(refresh, 4000);
  } catch (e) {
    error.value = e.message;
    loading.value = false;
  }
});
onUnmounted(() => {
  clearInterval(poll);
  clearTimeout(toastTimer);
  clearPhotos(taskPhotos);
  clearPhotos(revisionPhotos);
});
const nav = [
  { id: "overview", label: "Обзор", icon: LayoutDashboard },
  { id: "content", label: "Публикации", icon: Layers },
  { id: "schedule", label: "Расписание", icon: CalendarClock },
  { id: "settings", label: "Компания", icon: Building2 },
];
</script>

<template>
  <div v-if="loading" class="splash">
    <div class="brand-mark"><Sparkles :size="25" /></div>
    <LoaderCircle class="spin" />
    <p>Подключаем ваш рабочий кабинет</p>
  </div>
  <div v-else-if="!state" class="splash">
    <div class="brand-mark"><ShieldCheck /></div>
    <h1>Ваш маркетолог — в Telegram</h1>
    <p>{{ error || "Откройте приложение кнопкой «Открыть» в боте." }}</p>
    <a class="button primary" href="https://t.me/awesomesmm_ai"
      >Открыть бота <ArrowUpRight :size="16" /></a
    ><button class="button" @click="refresh">Повторить</button>
  </div>
  <div v-else class="app-shell">
    <aside class="sidebar">
      <a class="brand" @click="tab = 'overview'" aria-label="Контент-студия — главная">
        <span class="studio-mark"><Layers :size="24" /></span><span class="studio-name">Контент<br />студия</span>
      </a>
      <span class="sidebar-caption">ВАШ AI-МАРКЕТОЛОГ</span>
      <div class="company-switch" @click="companyMenu = !companyMenu">
        <span class="company-avatar">{{
          company?.name?.slice(0, 1) || "A"
        }}</span><span class="company-label"
          ><b>{{ company?.name }}</b
          ><small>Рабочее пространство</small></span
        ><ChevronDown :size="15" />
      </div>
      <div v-if="companyMenu" class="company-dropdown">
        <button v-for="c in state.companies" :key="c.id" @click="choose(c)">
          {{ c.name }}<Check v-if="c.id === current" :size="16" /></button
        ><button
          v-if="state.user.admin"
          @click="
            form = { name: '', brief: '', tone: '', audience: '', channel: '' };
            companyModal = true;
            companyMenu = false;
          "
        >
          <Plus :size="16" /> Новая компания
        </button>
      </div>
      <nav>
        <button
          v-for="item in nav"
          :class="{ active: tab === item.id }"
          @click="tab = item.id"
        >
          <component :is="item.icon" :size="19" /><span>{{ item.label }}</span
          ><span
            v-if="item.id === 'content' && pending.length"
            class="nav-count"
            >{{ pending.length }}</span
          >
        </button>
      </nav>
      <div class="sidebar-bottom">
        <div class="connection">
          <span class="online-dot"></span><b>ChatGPT подключён</b
          ><small>Через существующий аккаунт</small>
        </div>
        <div class="account">
          <span class="user-avatar">{{
            state.user.first_name?.slice(0, 1) || "A"
          }}</span>
          <div>
            <b>{{ state.user.first_name || "Администратор" }}</b><small>{{
              state.user.admin ? "Администратор" : "Участник"
            }}</small>
          </div>
          <ShieldCheck :size="17" />
        </div>
      </div>
    </aside>
    <main>
      <header>
        <div class="breadcrumb">
          {{ company?.name || "Рабочее пространство" }}
          <ChevronRight :size="14" />
          <b>{{ nav.find((n) => n.id === tab)?.label }}</b>
        </div>
        <div class="header-right">
          <span class="live-indicator"
            ><span class="online-dot"></span> Система активна</span
          ><button
            class="icon-button"
            title="Публикации на согласовании"
            @click="tab = 'content'"
          >
            <Bell :size="19" /><i v-if="pending.length"></i>
          </button>
        </div>
      </header>
      <div class="page">
        <div v-if="error" class="error-banner" role="alert">
          <CircleHelp :size="18" /><span>{{ error }}</span
          ><button @click="error = ''" aria-label="Закрыть">
            <X :size="16" />
          </button>
        </div>
        <template v-if="tab === 'overview'">
          <div class="page-heading">
            <div>
              <div class="eyebrow">РАБОЧЕЕ ПРОСТРАНСТВО</div>
              <h1>Ваш контент<span>.</span></h1>
              <p>Ставьте задачи, согласовывайте материалы и планируйте публикации.</p>
            </div>
            <button
              class="button secondary desktop-only"
              @click="tab = 'schedule'"
            >
              <CalendarClock :size="16" /> Расписание
            </button>
          </div>
          <section
            v-if="schedules.some((s) => s.enabled)"
            class="automation-banner"
          >
            <span class="mini-icon"><CalendarClock :size="19" /></span>
            <div>
              <b>Автоподготовка включена</b>
              <p>
                По расписанию AI создаёт новые материалы и присылает их на
                согласование.
              </p>
            </div>
            <button
              class="button secondary"
              :disabled="busy"
              @click="pauseAllSchedules"
            >
              <Pause :size="15" /> Остановить
            </button>
          </section>
          <section class="stats-grid">
            <div class="stat">
              <span>На согласовании <span class="stat-dot purple"></span></span
              ><strong>{{ pending.length.toString().padStart(2, "0") }}</strong
              ><small>{{
                pending.length ? "Ждут вашего решения" : "Всё согласовано"
              }}</small>
            </div>
            <div class="stat">
              <span>Опубликовано <span class="stat-dot green"></span></span
              ><strong>{{
                published.length.toString().padStart(2, "0")
              }}</strong
              ><small>Реальные публикации в канале</small>
            </div>
            <div class="stat">
              <span>В работе <span class="stat-dot orange"></span></span
              ><strong>{{ working.length.toString().padStart(2, "0") }}</strong
              ><small>{{
                working.length
                  ? "ChatGPT готовит материалы"
                  : "Готовы к новым задачам"
              }}</small>
            </div>
            <div class="stat">
              <span>По расписанию <span class="stat-dot blue"></span></span
              ><strong>{{
                schedules
                  .filter((s) => s.enabled)
                  .length.toString()
                  .padStart(2, "0")
              }}</strong
              ><small>Автоматическая подготовка</small>
            </div>
          </section>
          <div class="dashboard-grid">
            <section class="panel composer">
              <div class="panel-title">
                <span class="mini-icon"><Sparkles :size="17" /></span>
                <h3>Что будем продвигать?</h3>
                <span class="tiny-badge">НОВАЯ ЗАДАЧА</span>
              </div>
              <p>Опишите идею своими словами — остальное сделает AI.</p>
              <textarea
                v-model="task"
                maxlength="12000"
                placeholder="Например: подготовь тёплый пост о том, почему за качеством стекла всегда стоит команда. Без выдуманных имён и событий."
                @keydown.ctrl.enter.prevent="createTask"
              ></textarea>
              <div class="photo-controls">
                <label class="photo-upload"><ImageIcon :size="17" /> Свои фотографии
                  <input type="file" accept="image/jpeg,image/png" multiple :disabled="uploadingPhotos" @change="addPhotos($event)" />
                </label>
                <small>{{ uploadingPhotos ? 'Загружаем…' : 'До 6 фото · JPG / PNG' }}</small>
              </div>
              <div v-if="taskPhotos.length" class="photo-previews">
                <figure v-for="(photo, index) in taskPhotos" :key="photo.id"><img :src="photo.preview" :alt="photo.name" /><button type="button" :aria-label="'Убрать фото ' + (index + 1)" @click="removeTaskPhoto(index)"><X :size="13" /></button></figure>
              </div>
              <p v-if="taskPhotos.length" class="photo-help">AI впишет фотографии в общую композицию, согласует фон и свет.</p>
              <fieldset class="image-format">
                <legend>Формат изображения</legend>
                <div class="format-options">
                  <button v-for="format in imageFormats" :key="format.value" type="button" :class="['format-choice', { active: taskRatio === format.value }]" :aria-pressed="taskRatio === format.value" @click="taskRatio = format.value">
                    <i :class="['format-shape', format.shape]" aria-hidden="true"></i><span><b>{{ format.value }}</b><small>{{ format.label }}</small></span>
                  </button>
                </div>
                <small class="format-help">1:1 — универсальный · 4:5 — крупнее в ленте · 16:9 — для панорам</small>
              </fieldset>
              <details class="post-brand-overrides"><summary>Логотип и стиль для этого поста</summary>
                <p class="field-note">По умолчанию — из настроек компании. Здесь можно заменить только для этой задачи.</p>
                <div v-if="taskLogo" class="brand-logo-row"><Creative :name="taskLogo" asset /><button type="button" class="text-button" @click="taskLogo = ''">Использовать логотип компании</button></div>
                <label class="photo-upload"><Upload :size="15" /> Другой логотип<input type="file" accept="image/png,image/jpeg" :disabled="uploadingPhotos" @change="uploadBrandFiles($event, 'task-logo')" /></label>
                <div class="brand-style-previews"><figure v-for="(id, index) in taskStyles" :key="id"><Creative :name="id" asset /><button type="button" :aria-label="'Убрать пример поста ' + (index + 1)" @click="taskStyles.splice(index, 1)"><X :size="13" /></button></figure></div>
                <label class="photo-upload"><ImageIcon :size="15" /> Примеры для этого поста<input type="file" accept="image/png,image/jpeg" multiple :disabled="uploadingPhotos" @change="uploadBrandFiles($event, 'task-style')" /></label>
              </details>
              <div class="composer-footer">
                <span><ShieldCheck :size="14" /> Сначала ваше одобрение</span
                ><button
                  class="button primary"
                  @click="createTask"
                  :disabled="busy || uploadingPhotos || !task.trim()"
                >
                  <LoaderCircle v-if="busy" class="spin" :size="16" /><Sparkles
                    v-else
                    :size="16"
                  />
                  Создать материал <ArrowRight :size="16" />
                </button>
              </div>
              <div class="suggestions">
                <button
                  @click="
                    task = 'Предложи контент-план на неделю для нашей компании'
                  "
                >
                  Контент-план</button
                ><button
                  @click="
                    task =
                      isSalavat ? 'Подготовь пост для коллег о том, что за качеством стекла стоит слаженная команда. Используй только подтверждённые ценности из брендбука, без выдуманных событий и персоналий.' : 'Подготовь знакомящий пост о нашей компании и её преимуществах'
                  "
                >
                  История команды</button
                ><button
                  @click="
                    task =
                      'Предложи 5 тем публикаций для нашей целевой аудитории'
                  "
                >
                  Идеи для постов
                </button>
              </div>
            </section>
            <section class="panel activity-panel">
              <div class="panel-title">
                <h3>Сейчас в работе</h3>
                <span class="subtle-live"
                  ><span class="online-dot"></span> LIVE</span
                >
              </div>
              <div v-if="!events.length" class="empty-small">
                События появятся после первой задачи
              </div>
              <div
                v-for="event in events.slice(0, 4)"
                :key="event.id"
                class="activity-row"
              >
                <div class="activity-symbol" :class="event.kind">
                  <Check v-if="event.kind === 'published'" :size="14" /><Clock3
                    v-else-if="event.kind === 'schedule'"
                    :size="14"
                  /><Sparkles v-else :size="14" />
                </div>
                <div>
                  <p>{{ event.text }}</p>
                  <small>{{ fmt(event.at) }}</small>
                </div>
              </div>
              <div class="activity-note">
                <ShieldCheck :size="15" /> Все действия сохраняются в журнале
              </div>
            </section>
          </div>
          <section class="recent-section">
            <div class="section-title">
              <h3>
                Последние материалы <span>{{ posts.length }}</span>
              </h3>
              <button class="text-button" @click="tab = 'content'">
                Все публикации <ArrowRight :size="15" />
              </button>
            </div>
            <div v-if="!posts.length" class="empty-state">
              <div class="empty-icon"><Layers :size="25" /></div>
              <h3>Здесь начнётся ваша история</h3>
              <p>
                Поставьте первую задачу — материал появится здесь и в Telegram.
              </p>
            </div>
            <div v-else class="post-grid">
              <article
                v-for="post in posts.slice(0, 3)"
                :key="post.id"
                class="post-card"
                @click="selected = post"
              >
                <div class="post-visual">
                  <Creative v-if="post.image" :name="post.image" />
                  <div v-else class="post-placeholder">
                    <LoaderCircle
                      v-if="['working', 'generating'].includes(post.status)"
                      class="spin"
                      :size="32"
                    /><FileText v-else :size="32" />
                  </div>
                  <span class="status-badge" :class="post.status">{{
                    labels[post.status]
                  }}</span>
                </div>
                <div class="post-copy">
                  <small
                    >TELEGRAM <span>·</span> {{ fmt(post.created_at) }}</small
                  >
                  <h3>{{ post.title || "Готовим ваш материал" }}</h3>
                  <p>{{ post.body || post.request }}</p>
                  <div class="post-card-footer">
                    <span>Версия {{ post.revision }}</span
                    ><ArrowUpRight :size="17" />
                  </div>
                </div>
              </article>
            </div>
          </section>
        </template>
        <template v-if="tab === 'content'"
          ><div class="page-heading">
            <div>
              <div class="eyebrow">ОТ ЧЕРНОВИКА ДО РЕЗУЛЬТАТА</div>
              <h1>Публикации<span>.</span></h1>
              <p>Материалы, согласования и история вашего контента.</p>
            </div>
            <button class="button primary" @click="tab = 'overview'">
              <Plus :size="17" /> Новый материал
            </button>
          </div>
          <div class="content-summary">
            <span>{{ posts.length }} всего</span
            ><span class="purple-text"
              >{{ pending.length }} на согласовании</span
            ><span>{{ published.length }} опубликовано</span>
          </div>
          <div v-if="!posts.length" class="empty-state">
            <Layers :size="32" />
            <h3>Пока нет материалов</h3>
            <p>Создайте первую задачу на странице обзора или напишите боту.</p>
          </div>
          <div class="post-grid">
            <article
              v-for="post in posts"
              :key="post.id"
              class="post-card"
              @click="selected = post"
            >
              <div class="post-visual">
                <Creative v-if="post.image" :name="post.image" />
                <div v-else class="post-placeholder">
                  <LoaderCircle
                    v-if="['working', 'generating'].includes(post.status)"
                    class="spin"
                    :size="32"
                  /><FileText v-else :size="32" />
                </div>
                <span class="status-badge" :class="post.status">{{
                  labels[post.status]
                }}</span>
              </div>
              <div class="post-copy">
                <small>TELEGRAM · {{ fmt(post.created_at) }}</small>
                <h3>{{ post.title || "Готовим ваш материал" }}</h3>
                <p>{{ post.body || post.request }}</p>
                <div class="post-card-footer">
                  <span>Версия {{ post.revision }}</span
                  ><ArrowUpRight :size="17" />
                </div>
              </div>
            </article></div
        ></template>
        <template v-if="tab === 'settings'"
          ><div class="page-heading">
            <div>
              <div class="eyebrow">ОСНОВА ХОРОШЕГО КОНТЕНТА</div>
              <h1>Знания о компании<span>.</span></h1>
              <p>Расскажите о бизнесе. AI учтёт это в каждой задаче.</p>
            </div>
            <button
              v-if="state.user.admin"
              class="button secondary"
              @click="
                form = {
                  name: '',
                  brief: '',
                  tone: '',
                  audience: '',
                  channel: '',
                };
                companyModal = true;
              "
            >
              <Plus :size="16" /> Компания
            </button>
          </div>
          <div class="settings-layout">
            <form class="panel settings-form" @submit.prevent="saveCompany">
              <label
                >Название компании<input
                  v-model="form.name"
                  required
                  maxlength="100" /></label
              ><label
                >О компании и продуктах<textarea
                  v-model="form.brief"
                  rows="8"
                  placeholder="Продукты, услуги, преимущества, факты, ограничения…"
                ></textarea></label
              ><label class="upload-zone"
                ><Upload :size="17" /><span
                  >Добавить TXT / MD в базу знаний</span
                ><input type="file" accept=".txt,.md" @change="readFile"
              /></label>
              <p class="field-note">
                PDF можно отправить в чат боту. Ссылки на сайты пока добавляйте
                вместе с текстом нужных страниц.
              </p>
              <label
                >Целевая аудитория<input
                  v-model="form.audience"
                  placeholder="Для кого мы создаём контент" /></label
              ><label
                >Стиль общения<textarea
                  v-model="form.tone"
                  rows="3"
                  placeholder="Тон, примеры, что можно и чего нельзя"
                ></textarea></label
              ><label
                >Telegram-канал<input
                  v-model="form.channel"
                  placeholder="@channel_username"
              /></label>
              <p class="field-note">
                Бот должен быть администратором канала с правом публикации.
              </p>
              <section class="brand-settings-fields">
                <h3>Визуальный стиль и эмодзи</h3>
                <label>Логотип компании</label>
                <p class="field-note">Будет органично включаться в каждое новое изображение. PNG с прозрачным фоном — лучший вариант.</p>
                <div v-if="branding.logo_asset_id" class="brand-logo-row"><Creative :name="branding.logo_asset_id" asset /><button type="button" class="text-button" @click="branding.logo_asset_id = ''">Убрать логотип</button></div>
                <label class="photo-upload"><Upload :size="16" /> {{ branding.logo_asset_id ? 'Заменить логотип' : 'Загрузить логотип' }}<input type="file" accept="image/png,image/jpeg" :disabled="uploadingPhotos" @change="uploadBrandFiles($event, 'company-logo')" /></label>
                <label>Примеры изображений</label>
                <p class="field-note">Загрузите до 8 примеров нужного стиля. AI будет учитывать их композицию, цвета и оформление, без переноса старых фактов.</p>
                <div class="brand-style-previews"><figure v-for="(id, index) in branding.style_assets" :key="id"><Creative :name="id" asset /><button type="button" :aria-label="'Убрать пример ' + (index + 1)" @click="branding.style_assets.splice(index, 1)"><X :size="13" /></button></figure></div>
                <label class="photo-upload"><ImageIcon :size="16" /> Добавить примеры стиля<input type="file" accept="image/png,image/jpeg" multiple :disabled="uploadingPhotos" @change="uploadBrandFiles($event, 'company-style')" /></label>
                <label>Пожелания к изображениям<textarea v-model="branding.style_notes" rows="3" maxlength="6000" placeholder="Что взять из примеров: композицию, цвета, шрифты, настроение. Что не использовать." /></label>
                <label>Эмодзи в публикациях<select v-model="branding.emoji_source"><option value="references">Из пересланных примеров компании</option><option value="pack">Выбранный Telegram-пак</option><option value="standard">Обычные эмодзи</option></select></label>
                <template v-if="branding.emoji_source === 'pack'"><label>Ссылка или название эмодзи-пака<input v-model="branding.emoji_pack" maxlength="200" placeholder="https://t.me/addemoji/название" /></label><p class="field-note">Подойдёт ссылка «Поделиться» на набор эмодзи или его короткое название. Набор загрузится при сохранении.</p></template>
                <p v-if="company.emoji_source === 'pack' && company.emoji_title" class="emoji-pack-status">Подключён: {{ company.emoji_title }} · {{ company.emoji_items?.length || 0 }} эмодзи</p>
              </section>
              <label
                >Участники компании · Telegram ID через запятую<input
                  v-model="form.members_text"
                  placeholder="123456789, 987654321"
              /></label>
              <p class="field-note">
                Участники получают доступ только к этой компании. Владелец и
                администратор сохраняют доступ.
              </p>
              <button class="button primary" :disabled="busy || uploadingPhotos">
                <Check :size="17" /> Сохранить настройки
              </button>
            </form>
            <aside class="panel knowledge-tip">
              <span class="tip-icon"><ShieldCheck :size="25" /></span>
              <h3>Ваши знания — только ваши</h3>
              <p>
                У каждой компании своя база знаний, история, публикации и
                согласования.
              </p>
              <hr />
              <h4>Что добавить для начала</h4>
              <ul>
                <li>Чем занимается компания</li>
                <li>Что важно вашим клиентам</li>
                <li>Факты о продуктах и услугах</li>
                <li>Примеры хороших публикаций</li>
                <li>Запреты и требования бренда</li>
              </ul>
              <div class="provider-note">
                <Bot :size="18" />
                <div>
                  <b>{{ state.provider }}</b><small>{{
                    state.image_provider
                  }}</small>
                </div>
              </div>
            </aside>
          </div></template
        >
        <template v-if="tab === 'schedule'"
          ><div class="page-heading">
            <div>
              <div class="eyebrow">РАБОТАЕТ, ДАЖЕ КОГДА ВЫ ЗАНЯТЫ</div>
              <h1>Всему своё время<span>.</span></h1>
              <p>AI сам начнёт задачу и пришлёт результат на согласование.</p>
            </div>
          </div>
          <div class="settings-layout">
            <form class="panel settings-form" @submit.prevent="addSchedule">
              <h3>Новая регулярная задача</h3>
              <label
                >Что подготовить?<textarea
                  v-model="schedule.request"
                  required
                  rows="4"
                  placeholder="Подготовь полезный пост для нашей аудитории"
                ></textarea></label
              ><label
                >Первый запуск<input
                  type="datetime-local"
                  v-model="schedule.next_run"
                  required /></label
              ><label
                >Повторять<select v-model="schedule.every_seconds">
                  <option :value="0">Один раз</option>
                  <option :value="30">
                    Каждые 30 секунд — для демонстрации
                  </option>
                  <option :value="3600">Каждый час</option>
                  <option :value="86400">Каждый день</option>
                  <option :value="604800">Каждую неделю</option>
                </select></label
              >
              <p class="field-note">
                Время указано в часовом поясе вашего устройства. Запуск готовит
                материал; публикация требует одобрения.
              </p>
              <button class="button primary" :disabled="busy">
                <CalendarClock :size="17" /> Запланировать
              </button>
            </form>
            <section class="panel schedule-list">
              <h3>Ваши расписания</h3>
              <div v-if="!schedules.length" class="empty-small">
                Первое расписание появится здесь
              </div>
              <article v-for="s in schedules" :key="s.id" class="schedule-item">
                <div class="schedule-top">
                  <span
                    class="status-badge"
                    :class="s.enabled ? 'approved' : 'answered'"
                    >{{
                      s.enabled
                        ? "Активно"
                        : s.every_seconds || s.every_minutes
                          ? "На паузе"
                          : "Выполнено"
                    }}</span
                  >
                  <div class="schedule-controls">
                    <button
                      class="button secondary"
                      :disabled="busy"
                      @click="toggleSchedule(s)"
                    >
                      <Pause v-if="s.enabled" :size="14" /><Play
                        v-else
                        :size="14"
                      />{{ s.enabled ? "Пауза" : "Включить" }}</button
                    ><button
                      class="icon-button"
                      @click="removeSchedule(s)"
                      title="Удалить расписание"
                    >
                      <X :size="16" />
                    </button>
                  </div>
                </div>
                <p>{{ s.request }}</p>
                <small
                  ><Clock3 :size="13" /> {{ fmt(s.next_run) }} ·
                  {{
                    s.every_seconds || s.every_minutes
                      ? s.every_seconds
                        ? `каждые ${s.every_seconds} сек.`
                        : `каждые ${s.every_minutes} мин.`
                      : "однократно"
                  }}</small
                >
              </article>
            </section>
          </div></template
        >
        <footer>
          <span
            >Контент-студия <span class="footer-divider">/</span> От идеи до публикации.</span
          ><span><ShieldCheck :size="13" /> Публикуем только с одобрением</span>
        </footer>
      </div>
    </main>
    <nav class="mobile-nav">
      <button
        v-for="item in nav"
        :class="{ active: tab === item.id }"
        @click="tab = item.id"
      >
        <component :is="item.icon" :size="20" /><span>{{
          item.label
        }}</span></button
      ><button @click="companyMenu = !companyMenu">
        <ChevronDown :size="20" /><span>Компании</span>
      </button>
    </nav>
    <div v-if="companyMenu" class="mobile-company-menu">
      <button v-for="c in state.companies" @click="choose(c)">
        {{ c.name }}<Check v-if="c.id === current" :size="16" /></button
      ><button
        v-if="state.user.admin"
        @click="
          form = { name: '', brief: '', tone: '', audience: '', channel: '' };
          companyModal = true;
          companyMenu = false;
        "
      >
        <Plus :size="16" /> Добавить компанию
      </button>
    </div>
    <div
      v-if="selected"
      class="modal-backdrop"
      @click.self="
        selected = null;
        editing = false;
      "
    >
      <section class="modal post-modal">
        <div class="modal-head">
          <span class="status-badge" :class="selected.status">{{
            labels[selected.status]
          }}</span><button
            class="icon-button"
            @click="
              selected = null;
              editing = false;
            "
            aria-label="Закрыть"
          >
            <X :size="21" />
          </button>
        </div>
        <div class="post-detail">
          <Creative v-if="selected.image" :name="selected.image" />
          <div class="detail-copy">
            <div class="eyebrow">
              ВЕРСИЯ {{ selected.revision }} ·
              {{ company?.channel || "КАНАЛ НЕ ПОДКЛЮЧЁН" }}
            </div>
            <h2>{{ selected.title || "Материал готовится" }}</h2>
            <PostText v-if="!editing" :post="selected" />
            <textarea
              v-else
              v-model="editBody"
              rows="12"
              maxlength="3800"
            ></textarea>
            <div v-if="selected.error" class="error-banner">
              {{ selected.error }}
            </div>
            <a
              v-if="selected.url"
              :href="selected.url"
              target="_blank"
              class="button primary"
              >Посмотреть публикацию <ExternalLink :size="16"
            /></a>
            <div
              v-if="['review', 'approved'].includes(selected.status)"
              class="approval-actions"
            >
              <button
                v-if="selected.status === 'review' && !editing"
                class="button primary"
                :disabled="busy"
                @click="action(selected, 'approve')"
              >
                <Check :size="17" /> Одобрить и опубликовать</button
              ><button
                v-if="!editing"
                class="button secondary"
                @click="
                  editing = true;
                  editBody = selected.body;
                "
              >
                Редактировать текст</button
              ><button
                v-if="editing"
                class="button primary"
                :disabled="busy"
                @click="action(selected, 'edit')"
              >
                Сохранить новую версию</button
              ><button
                class="button danger-text"
                :disabled="busy"
                @click="action(selected, 'reject')"
              >
                Отклонить
              </button>
            </div>
            <div
              v-if="
                ['review', 'approved', 'rejected'].includes(selected.status)
              "
              class="revision-box"
            >
              <fieldset class="revision-scope"><legend>Что доработать</legend>
                <label><input type="radio" v-model="revisionScope" value="all" /> Текст и изображение</label>
                <label><input type="radio" v-model="revisionScope" value="text" /> Только текст</label>
                <label><input type="radio" v-model="revisionScope" value="image" /> Только изображение</label>
              </fieldset>
              <p v-if="revisionScope === 'text'" class="photo-help">Текущее изображение сохранится. Новая генерация картинки не запускается.</p>
              <label
                >Вернуть AI на доработку<textarea
                  v-model="revisionComment"
                  rows="2"
                  placeholder="Что изменить в материале?"
                ></textarea></label
              >
              <div v-if="revisionScope !== 'text'" class="revision-visual-controls">
              <div class="photo-controls">
                <label class="photo-upload"><ImageIcon :size="16" /> Добавить свои фото
                  <input type="file" accept="image/jpeg,image/png" multiple :disabled="uploadingPhotos" @change="addPhotos($event, true)" />
                </label>
                <small>{{ uploadingPhotos ? 'Загружаем…' : 'Одно фото или несколько' }}</small>
              </div>
              <div v-if="revisionPhotos.length" class="photo-previews">
                <figure v-for="(photo, index) in revisionPhotos" :key="photo.id"><img :src="photo.preview" :alt="photo.name" /><button type="button" :aria-label="'Убрать фото ' + (index + 1)" @click="removeRevisionPhoto(index)"><X :size="13" /></button></figure>
              </div>
              <p v-if="revisionPhotos.length" class="photo-help">Фото участвуют в самой генерации нового изображения.</p>
              <fieldset class="image-format">
                <legend>Формат изображения</legend>
                <div class="format-options">
                  <button v-for="format in imageFormats" :key="format.value" type="button" :class="['format-choice', { active: revisionRatio === format.value }]" :aria-pressed="revisionRatio === format.value" @click="revisionRatio = format.value; revisionScope = 'image'">
                    <i :class="['format-shape', format.shape]" aria-hidden="true"></i><span><b>{{ format.value }}</b><small>{{ format.label }}</small></span>
                  </button>
                </div>
              </fieldset>

              <details class="post-brand-overrides"><summary>Заменить логотип или примеры</summary>
                <div v-if="revisionLogo" class="brand-logo-row"><Creative :name="revisionLogo" asset /><button type="button" class="text-button" @click="revisionLogo = ''">Отменить замену</button></div>
                <label class="photo-upload"><Upload :size="15" /> Логотип для доработки<input type="file" accept="image/png,image/jpeg" :disabled="uploadingPhotos" @change="uploadBrandFiles($event, 'revision-logo')" /></label>
                <div class="brand-style-previews"><figure v-for="(id, index) in revisionStyles" :key="id"><Creative :name="id" asset /><button type="button" :aria-label="'Убрать пример доработки ' + (index + 1)" @click="revisionStyles.splice(index, 1)"><X :size="13" /></button></figure></div>
                <label class="photo-upload"><ImageIcon :size="15" /> Примеры для доработки<input type="file" accept="image/png,image/jpeg" multiple :disabled="uploadingPhotos" @change="uploadBrandFiles($event, 'revision-style')" /></label>
              </details>
              </div>
              <button
                class="button secondary"
                :disabled="busy || uploadingPhotos || (revisionScope === 'text' && !revisionComment.trim()) || (!revisionComment.trim() && !revisionPhotos.length && revisionRatio === (selected.aspect_ratio || '1:1') && revisionScope !== 'image' && !revisionLogo && !revisionStyles.length)"
                @click="action(selected, 'revise', revisionComment)"
              >
                <RotateCcw :size="15" /> Доработать
              </button>
            </div>
            <button
              v-if="selected.status === 'failed'"
              class="button secondary"
              :disabled="busy"
              @click="action(selected, 'retry')"
            >
              <RotateCcw :size="16" /> Повторить генерацию</button
            ><small class="detail-note"
              >{{ fmt(selected.created_at) }} · {{ selected.provider
              }}<br />Изображение: {{ selected.image_source || 'AI-макет · PNG-карточка' }} · {{ selected.aspect_ratio || '1:1' }}</small
            >
          </div>
        </div>
      </section>
    </div>
    <div
      v-if="companyModal"
      class="modal-backdrop"
      @click.self="
        companyModal = false;
        setForm();
      "
    >
      <form class="modal company-modal" @submit.prevent="addCompany">
        <div class="modal-head">
          <h2>Новая компания</h2>
          <button
            type="button"
            class="icon-button"
            @click="
              companyModal = false;
              setForm();
            "
          >
            <X :size="20" />
          </button>
        </div>
        <p>Отдельные знания, история и согласования.</p>
        <button type="button" class="brand-template" @click="useSalavatTemplate">
          <Sparkles :size="25" />
          <span><b>Редакционный профиль</b><small>Голос, оформление и примеры постов</small></span>
          <ArrowRight :size="18" />
        </button>
        <label
          >Название<input
            v-model="form.name"
            required
            placeholder="Например: Студия стекла" /></label
        ><label
          >Чем занимается<textarea
            v-model="form.brief"
            rows="4"
            placeholder="Расскажите о бизнесе"
          ></textarea></label
        ><label
          >Стиль общения<input
            v-model="form.tone"
            placeholder="Спокойно и по делу" /></label
        ><label
          >Telegram-канал · необязательно<input
            v-model="form.channel"
            placeholder="@my_channel"
        /></label>
        <p v-if="error" class="danger-text">{{ error }}</p>
        <button class="button primary" :disabled="busy">
          <Plus :size="17" /> Создать компанию
        </button>
      </form>
    </div>
    <Transition name="toast"
      ><div v-if="toast" class="toast">
        <Check :size="17" />{{ toast }}
      </div></Transition
    >
  </div>
</template>
