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
  tg?.setHeaderColor("#f6f7f9");
  tg?.setBackgroundColor("#f6f7f9");
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
const schedule = ref({ request: "", next_run: "", every_minutes: 0 });
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
        data.posts.find((p) => p.id === selected.value.id) || selected.value;
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
const posts = computed(
  () => state.value?.posts.filter((p) => p.company_id === current.value) || [],
);
const pending = computed(() =>
  posts.value.filter((p) => p.status === "review"),
);
const published = computed(() =>
  posts.value.filter((p) => p.status === "published"),
);
const working = computed(() =>
  posts.value.filter((p) => ["working", "generating"].includes(p.status)),
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
    await api("tasks", { company_id: current.value, request: task.value });
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
    });
    revisionComment.value = "";
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
      every_minutes: Number(schedule.value.every_minutes),
    });
    schedule.value = { request: "", next_run: "", every_minutes: 0 };
    notify("Задача запланирована");
    await refresh();
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
  props: ["name"],
  setup(props) {
    const src = ref("");
    let old = "";
    const load = async () => {
      if (!props.name) return;
      try {
        const r = await fetch("/media/" + props.name, { headers: headers() });
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
            alt: "Визуал публикации",
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
      <a class="brand" @click="tab = 'overview'"
        ><span class="brand-mark"><Sparkles :size="20" /></span>awesome<span
          class="brand-dot"
          >.</span
        ></a
      >
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
              <div class="eyebrow">МЕНЬШЕ РУТИНЫ. БОЛЬШЕ СМЫСЛА.</div>
              <h1>Маркетинг на автопилоте<span>.</span></h1>
              <p>Ваш AI-маркетолог готовит контент. Вы задаёте направление.</p>
            </div>
            <button
              class="button secondary desktop-only"
              @click="tab = 'schedule'"
            >
              <CalendarClock :size="16" /> Расписание
            </button>
          </div>
          <section class="hero-card">
            <div class="hero-copy">
              <span class="hero-tag"
                ><Sparkles :size="14" /> ВАША КОМАНДА ИЗ ОДНОГО AI</span
              >
              <h2>От идеи до публикации.<br />В одном диалоге.</h2>
              <p>
                Знает ваш бизнес, помнит контекст и приходит<br
                  class="desktop-only"
                />
                за одобрением перед каждым постом.
              </p>
              <a
                href="https://t.me/awesomesmm_ai"
                target="_blank"
                class="hero-link"
                >Написать маркетологу <ArrowUpRight :size="17"
              /></a>
            </div>
            <div class="orb-art" aria-hidden="true">
              <div class="orb-ring r1"></div>
              <div class="orb-ring r2"></div>
              <div class="orb-ring r3"></div>
              <div class="orb-core">
                <Sparkles :size="56" stroke-width="1.4" />
              </div>
              <span class="floating-label f1"
                ><ShieldCheck :size="14" /> Всё под контролем</span
              ><span class="floating-label f2"
                ><Zap :size="14" /> Работает за вас</span
              ><span class="art-cross">✦</span>
            </div>
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
                placeholder="Например: подготовь пост о том, как наш сервис помогает бизнесу экономить время на маркетинге…"
                @keydown.ctrl.enter.prevent="createTask"
              ></textarea>
              <div class="composer-footer">
                <span><ShieldCheck :size="14" /> Сначала ваше одобрение</span
                ><button
                  class="button primary"
                  @click="createTask"
                  :disabled="busy || !task.trim()"
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
                      'Подготовь знакомящий пост о нашей компании и её преимуществах'
                  "
                >
                  Пост о компании</button
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
              <label
                >Участники компании · Telegram ID через запятую<input
                  v-model="form.members_text"
                  placeholder="123456789, 987654321"
              /></label>
              <p class="field-note">
                Участники получают доступ только к этой компании. Владелец и
                администратор сохраняют доступ.
              </p>
              <button class="button primary" :disabled="busy">
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
                >Повторять<select v-model="schedule.every_minutes">
                  <option :value="0">Один раз</option>
                  <option :value="5">Каждые 5 минут — для демонстрации</option>
                  <option :value="60">Каждый час</option>
                  <option :value="1440">Каждый день</option>
                  <option :value="10080">Каждую неделю</option>
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
                    >{{ s.enabled ? "Активно" : "Выполнено" }}</span><button
                    class="icon-button"
                    @click="removeSchedule(s)"
                    title="Удалить расписание"
                  >
                    <X :size="16" />
                  </button>
                </div>
                <p>{{ s.request }}</p>
                <small
                  ><Clock3 :size="13" /> {{ fmt(s.next_run) }} ·
                  {{
                    s.every_minutes
                      ? `каждые ${s.every_minutes} мин.`
                      : "однократно"
                  }}</small
                >
              </article>
            </section>
          </div></template
        >
        <footer>
          <span
            >awesome<span class="purple-text">.</span>
            <span class="footer-divider">/</span> Ваш маркетинг.
            Сверхвозможности AI.</span
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
            <p class="post-full" v-if="!editing">
              {{ selected.body || selected.request }}
            </p>
            <textarea
              v-else
              v-model="editBody"
              rows="12"
              maxlength="1000"
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
              <label
                >Вернуть AI на доработку<textarea
                  v-model="revisionComment"
                  rows="2"
                  placeholder="Что изменить в материале?"
                ></textarea></label
              ><button
                class="button secondary"
                :disabled="busy || !revisionComment.trim()"
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
              }}<br />Изображение: AI-макет, программная PNG-карточка</small
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
