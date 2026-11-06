import { assetPaths, assetResponse } from '@/lib/assets'

export const getStaticPaths = () => assetPaths('svg')
export const GET = assetResponse('svg')
