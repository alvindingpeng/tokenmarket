import { useState } from "react"
import { useTranslations } from 'use-intl'
import { useQuery } from '@tanstack/react-query'
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useLogin, useRegister, useSendEmailCode, useForgotPassword, registerConfigQueryOptions } from "@/api/user"
import { useAPIKeyLogin } from "@/api/apikey"
import { useCountdown } from '@/hooks/useCountdown'
import { useSiteConfig, DEFAULT_SITE_NAME } from "@/api/site"
import { SiteBanners } from "@/components/site-banners"
import Logo from "@/components/modules/logo"
import { ArrowLeft, KeyRound, Store, User } from "lucide-react"
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@/components/ui/tabs"

type LoginMode = 'user' | 'apikey' | 'register-user' | 'register-reseller' | 'forgot';

// contactHref 把联系方式认成可用的链接: 管理员既可能填邮箱也可能填群链接, 也可能只是纯文本。
function contactHref(value: string): string | undefined {
  if (/^https?:\/\//i.test(value)) return value;
  if (/^[\w.+-]+@[\w-]+\.[\w.-]+$/.test(value)) return `mailto:${value}`;
  return undefined;
}

// LoginForm 渲染用户密码、API Key 与注册表单；注册入口按后端开关显隐。
export function LoginForm({ onBack }: { onBack?: () => void }) {
  const t = useTranslations('login')
  const [mode, setMode] = useState<LoginMode>('user')
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [passwordConfirm, setPasswordConfirm] = useState("") // passwordConfirm 注册/找回密码的二次确认。
  const [email, setEmail] = useState("")
  const [emailCode, setEmailCode] = useState("")
  // codeCooldown 发码按钮倒计时: 交给 useCountdown 统一管理定时器生命周期(卸载即停表)。
  const { remaining: codeCooldown, start: startCooldown } = useCountdown()
  const [reason, setReason] = useState("") // reason 为渠道商注册的申请理由。
  const [trustDevice, setTrustDevice] = useState(false) // trustDevice 表示是否请求后端签发 30 天登录凭证。
  const [apiKey, setApiKey] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [registerNotice, setRegisterNotice] = useState<string | null>(null)

  // 注册开关由后端决定: 默认关闭, 管理员开启后登录页才出现注册入口。
  const { data: registerConfig } = useQuery(registerConfigQueryOptions)
  // 站点身份文案同样来自后端「系统信息配置」, 留空回退默认品牌名。
  const { data: site } = useSiteConfig()

  const loginMutation = useLogin()
  const apiKeyLoginMutation = useAPIKeyLogin()
  const registerUserMutation = useRegister('user')
  const registerResellerMutation = useRegister('reseller')
  const sendEmailCodeMutation = useSendEmailCode()
  const forgotPasswordMutation = useForgotPassword()

  // handleSendCode 发送邮箱验证码并启动 60 秒倒计时; 注册与找回密码共用。
  const handleSendCode = async () => {
    setError(null)
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
      setError(t('error.emailInvalid'))
      return
    }
    try {
      await sendEmailCodeMutation.mutateAsync({
        email,
        purpose: mode === 'forgot' ? 'reset' : 'register',
      })
      startCooldown(60)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : t('error.generic'))
    }
  }

  const isRegister = mode === 'register-user' || mode === 'register-reseller'
  const isForgot = mode === 'forgot'

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)

    try {
      if (mode === 'user') {
        await loginMutation.mutateAsync({
          username,
          password,
          expire: trustDevice ? -1 : 86400,
        })
      } else if (mode === 'apikey') {
        await apiKeyLoginMutation.mutateAsync(apiKey)
      } else if (mode === 'forgot') {
        // 找回密码: 邮箱验证码通过后重设密码(两遍输入), 成功后回登录页。
        await forgotPasswordMutation.mutateAsync({ email, email_code: emailCode, password, password_confirm: passwordConfirm })
        setRegisterNotice(t('resetSuccess'))
        setMode('user')
      } else {
        // 注册: 密码两遍 + 8 位最短 + 邮箱验证码, 服务端逐一复核。
        if (password.length < 8) {
          setError(t('error.passwordTooShort'))
          return
        }
        if (password !== passwordConfirm) {
          setError(t('error.passwordMismatch'))
          return
        }
        const mutation = mode === 'register-user' ? registerUserMutation : registerResellerMutation
        await mutation.mutateAsync({
          username,
          password,
          password_confirm: passwordConfirm,
          email,
          email_code: emailCode,
          reason: mode === 'register-reseller' ? reason : undefined,
        })
        setRegisterNotice(registerConfig?.approval_required ? t('registerPending') : t('registerSuccess'))
        setMode('user')
      }
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : t('error.generic')
      setError(errorMessage)
    }
  }

  const isPending = loginMutation.isPending || apiKeyLoginMutation.isPending
    || registerUserMutation.isPending || registerResellerMutation.isPending
    || forgotPasswordMutation.isPending

  const handleModeChange = (value: string) => {
    setMode(value as LoginMode)
    setError(null)
    setRegisterNotice(null)
  }

  return (
    <div className="min-h-screen flex animate-in items-center justify-center px-6 text-foreground fade-in duration-300">
      <div className="w-full max-w-sm space-y-8">
        {onBack && (
          <button
            type="button"
            onClick={onBack}
            className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" />
            {t('backToHome')}
          </button>
        )}
        <header className="flex flex-col items-center gap-3">
          <Logo size={48} />
          {/* 站点名称/描述/联系方式来自「系统信息配置」; 名称留空回退默认品牌。 */}
          <h1 className="text-2xl font-bold text-center">{site?.site_name?.trim() || DEFAULT_SITE_NAME}</h1>
          {!!site?.site_description?.trim() && (
            <p className="text-center text-sm text-muted-foreground">{site.site_description.trim()}</p>
          )}
          {!!site?.site_contact?.trim() && (
            <p className="text-center text-xs text-muted-foreground break-all">
              {contactHref(site.site_contact.trim()) ? (
                <a className="underline underline-offset-2 hover:text-foreground" href={contactHref(site.site_contact.trim())}>
                  {site.site_contact.trim()}
                </a>
              ) : site.site_contact.trim()}
            </p>
          )}
        </header>

        {/* 维护提示对未登录用户同样可见: 被维护模式挡住的人需要知道登不上的原因。
            公告按管理员设置说明只面向登录用户, 这里不展示。 */}
        <SiteBanners className="flex flex-none flex-col gap-2" includeAnnouncement={false} />

        <Tabs value={mode} onValueChange={handleModeChange}>
          <TabsList className="flex w-full gap-1 overflow-x-auto rounded-2xl bg-muted p-1">
            <TabsTrigger
              value="user"
              className="flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-xl px-2 py-2.5 text-xs font-medium transition-colors data-[state=active]:text-foreground data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground sm:gap-2 sm:px-4 sm:text-sm"
            >
              <User className="hidden size-4 sm:block" />
              {t('mode.user')}
            </TabsTrigger>
            <TabsTrigger
              value="apikey"
              className="flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-xl px-2 py-2.5 text-xs font-medium transition-colors data-[state=active]:text-foreground data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground sm:gap-2 sm:px-4 sm:text-sm"
            >
              <KeyRound className="hidden size-4 sm:block" />
              {t('mode.apikey')}
            </TabsTrigger>
            {registerConfig?.user_enabled && (
              <TabsTrigger
                value="register-user"
                className="flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-xl px-2 py-2.5 text-xs font-medium transition-colors data-[state=active]:text-foreground data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground sm:gap-2 sm:px-4 sm:text-sm"
              >
                <User className="hidden size-4 sm:block" />
                {t('mode.registerUser')}
              </TabsTrigger>
            )}
            {registerConfig?.reseller_enabled && (
              <TabsTrigger
                value="register-reseller"
                className="flex min-w-0 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-xl px-2 py-2.5 text-xs font-medium transition-colors data-[state=active]:text-foreground data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground sm:gap-2 sm:px-4 sm:text-sm"
              >
                <Store className="hidden size-4 sm:block" />
                {t('mode.registerReseller')}
              </TabsTrigger>
            )}
          </TabsList>

          <form onSubmit={handleSubmit} className="space-y-6 pt-2">
            <div className="-mx-3 p-3 py-6">
              <TabsContent value="user" className="space-y-6" style={{ overflow: 'visible' }}>
                <Field>
                  <FieldLabel htmlFor="username">{t('username')}</FieldLabel>
                  <Input
                    id="username"
                    type="text"
                    placeholder={t('usernamePlaceholder')}
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    required={mode === 'user'}
                    disabled={isPending}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="password">{t('password')}</FieldLabel>
                  <Input
                    id="password"
                    type="password"
                    placeholder={t('passwordPlaceholder')}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required={mode === 'user'}
                    disabled={isPending}
                  />
                </Field>
                <Field orientation="horizontal" data-disabled={isPending}>
                  <Checkbox
                    id="trust-device"
                    checked={trustDevice}
                    onCheckedChange={(checked) => setTrustDevice(checked === true)}
                    disabled={isPending}
                  />
                  <FieldLabel htmlFor="trust-device" className="text-muted-foreground">
                    {t('trustDevice')}
                  </FieldLabel>
                </Field>
              </TabsContent>
              <TabsContent value="apikey" style={{ overflow: 'visible' }}>
                <Field>
                  <FieldLabel htmlFor="apikey">{t('apikey')}</FieldLabel>
                  <Input
                    id="apikey"
                    type="password"
                    placeholder={t('apikeyPlaceholder')}
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    required={mode === 'apikey'}
                    disabled={isPending}
                  />
                </Field>
              </TabsContent>
              {(mode === 'register-user' || mode === 'register-reseller') && (
                <TabsContent value={mode} className="space-y-6" style={{ overflow: 'visible' }}>
                  <Field>
                    <FieldLabel htmlFor="register-username">{t('username')}</FieldLabel>
                    <Input
                      id="register-username"
                      type="text"
                      placeholder={t('usernamePlaceholder')}
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="register-email">{t('email')}</FieldLabel>
                    <Input
                      id="register-email"
                      type="email"
                      placeholder={t('emailPlaceholder')}
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="register-email-code">{t('emailCode')}</FieldLabel>
                    <div className="flex gap-2">
                      <Input
                        id="register-email-code"
                        type="text"
                        placeholder={t('emailCodePlaceholder')}
                        value={emailCode}
                        onChange={(e) => setEmailCode(e.target.value)}
                        required
                        disabled={isPending}
                      />
                      <Button
                        type="button"
                        variant="outline"
                        className="shrink-0"
                        disabled={isPending || codeCooldown > 0 || sendEmailCodeMutation.isPending}
                        onClick={handleSendCode}
                      >
                        {codeCooldown > 0 ? t('resendCode', { seconds: codeCooldown }) : t('sendCode')}
                      </Button>
                    </div>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="register-password">{t('password')}</FieldLabel>
                    <Input
                      id="register-password"
                      type="password"
                      placeholder={t('passwordPlaceholder')}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="register-password-confirm">{t('passwordConfirm')}</FieldLabel>
                    <Input
                      id="register-password-confirm"
                      type="password"
                      placeholder={t('passwordConfirmPlaceholder')}
                      value={passwordConfirm}
                      onChange={(e) => setPasswordConfirm(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  {mode === 'register-reseller' && (
                    <Field>
                      <FieldLabel htmlFor="register-reason">{t('reason')}</FieldLabel>
                      <Input
                        id="register-reason"
                        type="text"
                        placeholder={t('reasonPlaceholder')}
                        value={reason}
                        onChange={(e) => setReason(e.target.value)}
                        disabled={isPending}
                      />
                    </Field>
                  )}
                </TabsContent>
              )}
              {isForgot && (
                <TabsContent value="forgot" className="space-y-6" style={{ overflow: 'visible' }}>
                  <Field>
                    <FieldLabel htmlFor="forgot-email">{t('email')}</FieldLabel>
                    <Input
                      id="forgot-email"
                      type="email"
                      placeholder={t('emailPlaceholder')}
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="forgot-email-code">{t('emailCode')}</FieldLabel>
                    <div className="flex gap-2">
                      <Input
                        id="forgot-email-code"
                        type="text"
                        placeholder={t('emailCodePlaceholder')}
                        value={emailCode}
                        onChange={(e) => setEmailCode(e.target.value)}
                        required
                        disabled={isPending}
                      />
                      <Button
                        type="button"
                        variant="outline"
                        className="shrink-0"
                        disabled={isPending || codeCooldown > 0 || sendEmailCodeMutation.isPending}
                        onClick={handleSendCode}
                      >
                        {codeCooldown > 0 ? t('resendCode', { seconds: codeCooldown }) : t('sendCode')}
                      </Button>
                    </div>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="forgot-password">{t('newPassword')}</FieldLabel>
                    <Input
                      id="forgot-password"
                      type="password"
                      placeholder={t('passwordPlaceholder')}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="forgot-password-confirm">{t('passwordConfirm')}</FieldLabel>
                    <Input
                      id="forgot-password-confirm"
                      type="password"
                      placeholder={t('passwordConfirmPlaceholder')}
                      value={passwordConfirm}
                      onChange={(e) => setPasswordConfirm(e.target.value)}
                      required
                      disabled={isPending}
                    />
                  </Field>
                  <button
                    type="button"
                    onClick={() => handleModeChange('user')}
                    className="text-sm text-muted-foreground underline underline-offset-2 hover:text-foreground"
                  >
                    {t('backToLogin')}
                  </button>
                </TabsContent>
              )}
            </div>

            {registerNotice && <FieldDescription className="text-muted-foreground">{registerNotice}</FieldDescription>}
            {error && <FieldDescription className="text-destructive">{error}</FieldDescription>}

            <Button type="submit" disabled={isPending} className="w-full">
              {isPending ? t('button.loading') : (isRegister ? t('button.register') : isForgot ? t('button.reset') : t('button.submit'))}
            </Button>
            {/* 忘记密码入口只在密码登录态露出; API Key 与注册态不适用。 */}
            {mode === 'user' && (
              <button
                type="button"
                onClick={() => handleModeChange('forgot')}
                className="mx-auto block text-sm text-muted-foreground underline underline-offset-2 hover:text-foreground"
              >
                {t('forgotPassword')}
              </button>
            )}
          </form>
        </Tabs>
      </div>
    </div>
  )
}
