import { cn } from "@/lib/utils"

import { Loader2Icon, type LucideProps } from "lucide-preact"

function Spinner({ className, ...props }: LucideProps) {
  return (
    <Loader2Icon
      data-slot="spinner"
      role="status"
      aria-label="Loading"
      className={cn("size-4 animate-spin", className)}
      {...props}
    />
  )
}

export { Spinner }
