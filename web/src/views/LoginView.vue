<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import AppIcon from '@/components/AppIcon.vue'
import { describeError } from '@/api/request'
import { useAuthStore } from '@/stores/authStore'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const submitting = ref(false)
const errorText = ref('')

onMounted(() => {
  if (auth.authenticated) {
    void goAfterLogin()
  }
})

function redirectTarget(): string {
  const redirect = route.query.redirect
  if (typeof redirect === 'string' && redirect.startsWith('/') && !redirect.startsWith('/login')) {
    return redirect
  }
  return '/pipelines'
}

async function goAfterLogin(): Promise<void> {
  await router.replace(redirectTarget())
}

async function onSubmit(): Promise<void> {
  if (submitting.value) {
    return
  }
  if (!username.value.trim() || !password.value) {
    errorText.value = '请输入用户名与密码'
    return
  }
  submitting.value = true
  errorText.value = ''
  try {
    await auth.login(username.value.trim(), password.value)
    ElMessage.success('登录成功')
    await goAfterLogin()
  } catch (e) {
    errorText.value = describeError(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login">
    <aside class="login__aside">
      <div class="login__brand">
        <span class="login__logo"><AppIcon name="cube" :size="18" /></span>
        <span>CI Platform</span>
      </div>

      <div class="login__hero">
        <h1 class="login__headline">持续集成与交付<br />开箱即用</h1>
        <p class="login__lead">轻量 CI/CD 平台：任务 → 触发 → 构建 → 日志 → SSH 发布，全流程可视。</p>
        <ul class="login__features">
          <li><span class="login__check">✓</span> 本地工具链构建，Go / Maven / Node 直接编译打包</li>
          <li><span class="login__check">✓</span> Webhook 触发与实时控制台日志</li>
          <li><span class="login__check">✓</span> 纯文件存储，单二进制交付，无数据库</li>
        </ul>
      </div>

      <div class="login__aside-foot">轻量 · 可靠 · 无需数据库</div>
    </aside>

    <main class="login__main">
      <section class="login__card">
        <h2 class="login__welcome">欢迎回来</h2>
        <p class="login__subtitle">请使用管理员账号登录以继续</p>

        <el-alert
          v-if="errorText"
          class="login__error"
          type="error"
          :closable="false"
          show-icon
          :title="errorText"
        />

        <form class="login__form" @submit.prevent="onSubmit">
          <label class="login__field">
            <span class="login__label">用户名</span>
            <input v-model="username" class="login__input" type="text" autocomplete="username" placeholder="用户名" autofocus />
          </label>

          <label class="login__field">
            <span class="login__label">密码</span>
            <input
              v-model="password"
              class="login__input"
              type="password"
              autocomplete="current-password"
              placeholder="••••••"
            />
          </label>

          <button class="login__submit" type="submit" :disabled="submitting">
            {{ submitting ? '登录中…' : '登录' }}
          </button>
        </form>

      </section>

      <div class="login__copyright">© 2026 CI Platform · 内部使用</div>
    </main>
  </div>
</template>

<style scoped>
.login {
  display: flex;
  min-height: 100vh;
  background: var(--bg-app);
}
.login__aside {
  display: none;
  flex-direction: column;
  justify-content: space-between;
  width: 46%;
  max-width: 620px;
  padding: 44px 52px;
  color: #e2e8f0;
  background:
    radial-gradient(1200px 500px at -10% -20%, rgba(59, 130, 246, 0.35), transparent 60%),
    linear-gradient(150deg, #0f172a 0%, #1e293b 55%, #1d4ed8 140%);
}
.login__brand {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 18px;
  font-weight: 650;
  color: #fff;
}
.login__logo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border-radius: 11px;
  background: linear-gradient(135deg, #3b82f6, #2563eb);
  box-shadow: 0 8px 20px rgba(37, 99, 235, 0.45);
}
.login__hero {
  margin: auto 0;
}
.login__headline {
  font-size: 38px;
  line-height: 1.25;
  margin: 0 0 18px;
  color: #fff;
  letter-spacing: -0.02em;
}
.login__lead {
  font-size: 15px;
  line-height: 1.8;
  color: #cbd5e1;
  margin: 0 0 28px;
  max-width: 420px;
}
.login__features {
  list-style: none;
  margin: 0;
  padding: 0;
}
.login__features li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
  color: #e2e8f0;
  font-size: 14px;
}
.login__check {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: rgba(59, 130, 246, 0.25);
  color: #93c5fd;
  font-size: 12px;
}
.login__aside-foot {
  font-size: 12px;
  color: #64748b;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.login__main {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 32px 24px;
}
.login__card {
  width: 100%;
  max-width: 400px;
  padding: 36px 38px 30px;
  background: #fff;
  border: 1px solid var(--border-1);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-md);
}
.login__welcome {
  margin: 0 0 6px;
  font-size: 24px;
  font-weight: 650;
  color: var(--text-1);
  letter-spacing: -0.01em;
}
.login__subtitle {
  margin: 0 0 22px;
  color: var(--text-3);
  font-size: 13.5px;
}
.login__error {
  margin-bottom: 16px;
}
.login__form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.login__field {
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.login__label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-2);
}
.login__input {
  height: 40px;
  padding: 0 13px;
  border: 1px solid var(--border-1);
  border-radius: var(--radius-sm);
  font-size: 14px;
  color: var(--text-1);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.login__input:focus {
  border-color: var(--brand-500);
  box-shadow: 0 0 0 3px var(--brand-100);
}
.login__submit {
  height: 42px;
  margin-top: 6px;
  border: none;
  border-radius: var(--radius-sm);
  background: var(--brand-600);
  color: #fff;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s, box-shadow 0.15s;
}
.login__submit:hover:not(:disabled) {
  background: var(--brand-700);
  box-shadow: 0 6px 16px rgba(37, 99, 235, 0.32);
}
.login__submit:disabled {
  opacity: 0.7;
  cursor: not-allowed;
}
.login__copyright {
  margin-top: 26px;
  font-size: 12px;
  color: var(--text-4);
}

@media (min-width: 960px) {
  .login__aside {
    display: flex;
  }
}
</style>
