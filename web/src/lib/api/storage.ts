import { del, get, patch, post, put } from '@/lib/http'
import type {
  CreateStorageConfigRequest,
  DeleteStorageConfigResponse,
  DriverSchemaRequest,
  DriverSchemaResponse,
  PageData,
  StorageConfig,
  StorageDriver,
  TestStorageConfigResponse,
  UpdateStorageConfigRequest,
  UpdateStorageSecretsRequest,
  UpdateStorageSecretsResponse,
} from '@/types/api'

/**
 * 存储驱动（API.md §3）。**全部端点需要 admin**。
 *
 * 两条边界（DESIGN.md §5.3）：
 *  - 同一驱动类型可有多条实例（D64）：列表展示 `Name`，内部一律用 `UID`
 *  - **响应中绝不含密钥**（D78）：编辑时密钥字段留空 = 不修改
 */

export interface StorageConfigListParams {
  Page?: number
  PageSize?: number
  Enabled?: boolean
  Type?: string
  Keyword?: string
}

export const storageApi = {
  /** 可用驱动类型 + **服务端求值后的** 配置字段 schema（来自 agent 探测）。 */
  drivers(): Promise<{ Drivers: StorageDriver[] }> {
    return get<{ Drivers: StorageDriver[] }>('/storage/drivers')
  },

  /**
   * 按当前表单值**重新求值** schema（处理 `DependsOn` 联动）。
   *
   * ⚠️ 联动必须回到服务端：`choices` / `default` 可能是插件函数，
   * 前端**永不执行插件代码**（DESIGN.md §7.3）。
   */
  driverSchema(payload: DriverSchemaRequest): Promise<DriverSchemaResponse> {
    return post<DriverSchemaResponse>('/storage/drivers/schema', payload)
  },

  list(params: StorageConfigListParams = {}): Promise<PageData<StorageConfig>> {
    return get<PageData<StorageConfig>>('/storage/configs', { params })
  },

  get(uid: string): Promise<StorageConfig> {
    return get<StorageConfig>(`/storage/configs/${encodeURIComponent(uid)}`)
  },

  create(payload: CreateStorageConfigRequest): Promise<StorageConfig> {
    return post<StorageConfig>('/storage/configs', payload)
  },

  /** 更新元数据与模板（**不含密钥**；`PicgoConfigName` 创建后只读）。 */
  update(uid: string, payload: UpdateStorageConfigRequest): Promise<StorageConfig> {
    return patch<StorageConfig>(`/storage/configs/${encodeURIComponent(uid)}`, payload)
  },
  /** 单独更新凭据（与元数据分离，D78）。**只提交需变更的字段**。 */
  updateSecrets(
    uid: string,
    payload: UpdateStorageSecretsRequest,
  ): Promise<UpdateStorageSecretsResponse> {
    return put<UpdateStorageSecretsResponse>(
      `/storage/configs/${encodeURIComponent(uid)}/secrets`,
      payload,
    )
  },

  /** 删除。`force` 为真时忽略「有图片引用」的冲突。 */
  remove(uid: string, force = false): Promise<DeleteStorageConfigResponse> {
    return del<DeleteStorageConfigResponse>(`/storage/configs/${encodeURIComponent(uid)}`, {
      params: { Force: force },
    })
  },

  /** 置为全局默认（同事务把其他行置 false，并让 agent 切换当前上传器）。 */
  activate(uid: string): Promise<StorageConfig> {
    return post<StorageConfig>(`/storage/configs/${encodeURIComponent(uid)}/activate`)
  },

  /** 连通性测试（**HTTP 仍为 200**，看 `Ok` 字段判断结果）。 */
  test(uid: string): Promise<TestStorageConfigResponse> {
    return post<TestStorageConfigResponse>(`/storage/configs/${encodeURIComponent(uid)}/test`)
  },
}

/** 供上传页等处复用的「拿默认存储」便捷函数。 */
export async function fetchDefaultStorageConfig(): Promise<StorageConfig | undefined> {
  const page = await storageApi.list({ Page: 1, PageSize: 100, Enabled: true })
  return page.Items.find((item) => item.IsDefault) ?? page.Items[0]
}
