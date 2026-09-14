import { Loader2, MoreHorizontal, Plus, RotateCcw, Search, Users } from 'lucide-react'
import { useEffect, useState } from 'react'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { PageHeader } from '@/components/common/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Pagination } from '@/components/ui/pagination'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { useUsers } from '@/hooks/api'
import { t } from '@/i18n'
import { usersApi } from '@/lib/api'
import { formatBytes, formatDateTime, formatQuota } from '@/lib/format'
import { toApiError, type User } from '@/types/api'
import { useAuthStore } from '@/store/auth-store'

/**
 * 用户管理（DESIGN.md §3.3 `/admin/users`，admin）。
 *
 * 两条必须的前端保护（后端也会拒，但前端要提前禁用并说明原因）：
 *  - **不能删除自己**
 *  - **不能删除最后一个管理员**
 *
 * 配额单位是**字节**（D20）；`CapacityBytes = 0` 表示**不限额**。
 */
export function AdminUsersPage() {
  const currentUser = useAuthStore((state) => state.user)

  const [keywordInput, setKeywordInput] = useState('')
  const { items, total, page, pageSize, loading, error, setPage, setKeyword, refresh } = useUsers({
    PageSize: 20,
  })

  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [deleting, setDeleting] = useState<User | null>(null)
  const [resetting, setResetting] = useState<User | null>(null)
  const [plainPassword, setPlainPassword] = useState('')

  // 统计管理员数量：用于「不能删最后一个管理员」的前端判定
  const adminCount = items.filter((user) => user.Role === 'admin' && user.Status === 'active').length

  const confirmDelete = async () => {
    if (!deleting) return
    const result = await usersApi.remove(deleting.UID)
    toast.success(
      t('USER_DELETED', {
        uploads: result.DeletedUploads,
        freed: formatBytes(result.FreedBytes),
      }),
    )
    refresh()
  }

  const doResetPassword = async () => {
    if (!resetting) return
    const result = await usersApi.resetPassword(resetting.UID)
    setPlainPassword(result.Password)
    refresh()
  }

  return (
    <>
      <PageHeader
        title={t('NAV_ADMIN_USERS')}
        description={t('USER_DESC')}
        actions={
          <Button variant="brand" size="sm" onClick={() => setCreating(true)}>
            <Plus aria-hidden />
            {t('USER_CREATE')}
          </Button>
        }
      />

      <div className="mb-4 flex flex-wrap items-end gap-2">
        <div className="min-w-[14rem] flex-1 space-y-1">
          <Label htmlFor="user-keyword" className="text-xs text-muted-foreground">
            {t('USER_FILTER_KEYWORD')}
          </Label>
          <Input
            id="user-keyword"
            value={keywordInput}
            onChange={(event) => setKeywordInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') setKeyword(keywordInput.trim())
            }}
            placeholder={t('USER_FILTER_KEYWORD_PLACEHOLDER')}
          />
        </div>
        <Button variant="outline" onClick={() => setKeyword(keywordInput.trim())}>
          <Search aria-hidden />
          {t('COMMON_SEARCH')}
        </Button>
        <IconButton label={t('COMMON_REFRESH')} onClick={refresh}>
          <RotateCcw aria-hidden />
        </IconButton>
      </div>

      {loading && items.length === 0 ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className="h-12 w-full" />
          ))}
        </div>
      ) : error ? (
        <EmptyState
          title={t('USER_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <EmptyState
          icon={Users}
          title={t('USER_EMPTY')}
          description={t('USER_EMPTY_DESC')}
          action={
            <Button variant="brand" onClick={() => setCreating(true)}>
              {t('USER_CREATE')}
            </Button>
          }
        />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('SETTINGS_EMAIL')}</TableHead>
                  <TableHead>{t('SETTINGS_NICKNAME')}</TableHead>
                  <TableHead>{t('SETTINGS_ROLE')}</TableHead>
                  <TableHead>{t('SETTINGS_STATUS')}</TableHead>
                  <TableHead>{t('SETTINGS_QUOTA')}</TableHead>
                  <TableHead>{t('SETTINGS_LAST_LOGIN')}</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>

              <TableBody>
                {items.map((user) => {
                  const isSelf = user.UID === currentUser?.UID
                  const isLastAdmin = user.Role === 'admin' && adminCount <= 1
                  const deleteDisabled = isSelf || isLastAdmin

                  return (
                    <TableRow key={user.UID}>
                      <TableCell className="text-sm">
                        {user.Email}
                        {isSelf ? (
                          <Badge variant="brand" className="ml-2">
                            {t('USER_SELF_BADGE')}
                          </Badge>
                        ) : null}
                        {user.MustChangePassword ? (
                          <Badge variant="warning" className="ml-2">
                            {t('USER_MUST_CHANGE_PASSWORD')}
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {user.Nickname || '—'}
                      </TableCell>
                      <TableCell>
                        <Badge variant={user.Role === 'admin' ? 'brand' : 'secondary'}>
                          {user.Role === 'admin' ? t('USER_MENU_ROLE_ADMIN') : t('USER_MENU_ROLE_USER')}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={user.Status === 'active' ? 'success' : 'destructive'}>
                          {user.Status === 'active' ? t('STATUS_ACTIVE') : t('STATUS_DISABLED')}
                        </Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                        {formatQuota(user.UsedBytes, user.CapacityBytes)}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                        {formatDateTime(user.LastLoginAt)}
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <IconButton label={t('COMMON_MORE')} className="size-8">
                              <MoreHorizontal aria-hidden />
                            </IconButton>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onSelect={() => setEditing(user)}>
                              {t('COMMON_EDIT')}
                            </DropdownMenuItem>
                            <DropdownMenuItem onSelect={() => setResetting(user)}>
                              {t('USER_RESET_PASSWORD')}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              className="text-destructive focus:text-destructive"
                              disabled={deleteDisabled}
                              onSelect={() => setDeleting(user)}
                            >
                              {t('USER_DELETE')}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>

          <Pagination className="mt-4" page={page} pageSize={pageSize} total={total} onPageChange={setPage} />
        </>
      )}

      {/* 新建 / 编辑 */}
      <UserFormDialog
        open={creating || editing !== null}
        user={editing}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false)
            setEditing(null)
          }
        }}
        onSaved={() => {
          refresh()
          setCreating(false)
          setEditing(null)
        }}
      />

      {/* 重置密码：明文只显示一次 */}
      <Dialog
        open={resetting !== null}
        onOpenChange={(open) => {
          if (!open) {
            setResetting(null)
            setPlainPassword('')
          }
        }}
      >
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{t('USER_RESET_PASSWORD')}</DialogTitle>
            <DialogDescription>
              {t('USER_RESET_PASSWORD_DESC', { email: resetting?.Email ?? '' })}
            </DialogDescription>
          </DialogHeader>

          {plainPassword ? (
            <div className="space-y-2">
              <p className="text-sm font-medium text-warning">{t('TOKEN_PLAIN_ONCE')}</p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1 font-mono text-sm">
                  {plainPassword}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={() => {
                    void navigator.clipboard?.writeText(plainPassword)
                    toast.success(t('COMMON_COPIED'))
                  }}
                >
                  {t('COMMON_COPY')}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">{t('USER_RESET_PASSWORD_NOTE')}</p>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">{t('USER_RESET_PASSWORD_CONFIRM')}</p>
          )}

          <DialogFooter>
            <Button variant="outline" onClick={() => setResetting(null)}>
              {t('COMMON_CLOSE')}
            </Button>
            {plainPassword ? null : (
              <Button variant="brand" onClick={() => void doResetPassword()}>
                {t('COMMON_CONFIRM')}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 删除确认 */}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('USER_DELETE_TITLE', { email: deleting?.Email ?? '' })}
        description={t('USER_DELETE_DESC')}
        confirmLabel={t('USER_DELETE')}
        destructive
        onConfirm={confirmDelete}
      />
    </>
  )
}

// ---------------------------------------------------------------------------

interface UserFormDialogProps {
  open: boolean
  user: User | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}

function UserFormDialog({ open, user, onOpenChange, onSaved }: UserFormDialogProps) {
  const editing = user !== null

  const [email, setEmail] = useState('')
  const [nickname, setNickname] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('user')
  const [status, setStatus] = useState('active')
  /** 单位 GiB（界面友好）；0 = 不限额 */
  const [capacityGiB, setCapacityGiB] = useState('5')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    if (user) {
      setEmail(user.Email)
      setNickname(user.Nickname ?? '')
      setRole(user.Role)
      setStatus(user.Status)
      setCapacityGiB(user.CapacityBytes > 0 ? String(user.CapacityBytes / 1024 ** 3) : '0')
      setPassword('')
    } else {
      setEmail('')
      setNickname('')
      setPassword('')
      setRole('user')
      setStatus('active')
      setCapacityGiB('5')
    }
  }, [open, user])

  const submit = async () => {
    const trimmedEmail = email.trim()
    if (!trimmedEmail) {
      toast.error(t('USER_EMAIL_REQUIRED'))
      return
    }

    const gib = Number(capacityGiB)
    const capacityBytes = Number.isFinite(gib) && gib > 0 ? Math.round(gib * 1024 ** 3) : 0

    setBusy(true)
    try {
      if (editing && user) {
        await usersApi.update(user.UID, {
          Email: trimmedEmail,
          Nickname: nickname.trim(),
          Role: role,
          Status: status,
          CapacityBytes: capacityBytes,
          ...(password ? { NewPassword: password } : {}),
        })
        toast.success(t('USER_UPDATED'))
      } else {
        await usersApi.create({
          Email: trimmedEmail,
          Password: password,
          Nickname: nickname.trim(),
          Role: role,
          CapacityBytes: capacityBytes,
        })
        toast.success(t('USER_CREATED'))
      }
      onSaved()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? t('USER_EDIT_TITLE') : t('USER_CREATE_TITLE')}</DialogTitle>
          <DialogDescription>{t('USER_FORM_DESC')}</DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="user-email">{t('SETTINGS_EMAIL')}</Label>
            <Input
              id="user-email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="user-nickname">{t('SETTINGS_NICKNAME')}</Label>
            <Input
              id="user-nickname"
              value={nickname}
              onChange={(event) => setNickname(event.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="user-password">
              {editing ? t('USER_NEW_PASSWORD_OPTIONAL') : t('AUTH_LOGIN_PASSWORD')}
            </Label>
            <Input
              id="user-password"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            {editing ? (
              <p className="text-xs text-muted-foreground">{t('USER_NEW_PASSWORD_HINT')}</p>
            ) : null}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="user-role">{t('SETTINGS_ROLE')}</Label>
              <Select value={role} onValueChange={setRole}>
                <SelectTrigger id="user-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="user">{t('USER_MENU_ROLE_USER')}</SelectItem>
                  <SelectItem value="admin">{t('USER_MENU_ROLE_ADMIN')}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {editing ? (
              <div className="space-y-1.5">
                <Label htmlFor="user-status">{t('SETTINGS_STATUS')}</Label>
                <Select value={status} onValueChange={setStatus}>
                  <SelectTrigger id="user-status">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="active">{t('STATUS_ACTIVE')}</SelectItem>
                    <SelectItem value="disabled">{t('STATUS_DISABLED')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="user-capacity">{t('USER_CAPACITY_GIB')}</Label>
            <Input
              id="user-capacity"
              type="number"
              min="0"
              step="1"
              value={capacityGiB}
              onChange={(event) => setCapacityGiB(event.target.value)}
            />
            <p className="text-xs text-muted-foreground">{t('USER_CAPACITY_HINT')}</p>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {t('COMMON_CANCEL')}
          </Button>
          <Button variant="brand" onClick={() => void submit()} disabled={busy}>
            {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {t('COMMON_SAVE')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
