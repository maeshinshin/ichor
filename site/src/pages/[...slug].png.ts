import { assetPaths, assetResponse } from '@/lib/assets'

export const getStaticPaths = () => assetPaths('png')
export const GET = assetResponse('png')
