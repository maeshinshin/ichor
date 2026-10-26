import { isValidElement, type ComponentChildren, type CSSProperties, type TargetedMouseEvent } from "preact"
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks"
import { sonnerStyles } from "./internal/sonner-styles"

// Port of sonner 2.0.8 to Preact: same markup, data attributes, CSS variables and behavior.
// shortcut: no toast.promise, toast.custom, toast.message, toast.getHistory/getToasts or useSonner, the Toaster takes no ref, and there is no matchMedia addListener fallback for Safari < 14; port them from sonner when an app needs them.

if (typeof document !== "undefined") {
  const style = document.createElement("style")
  style.textContent = sonnerStyles
  document.head.appendChild(style)
}

type Position = "top-left" | "top-right" | "bottom-left" | "bottom-right" | "top-center" | "bottom-center"
type SwipeDirection = "top" | "right" | "bottom" | "left"
type ToastTypes = "success" | "info" | "warning" | "error" | "loading"
type Title = (() => ComponentChildren) | ComponentChildren
type Offset = { top?: string | number; right?: string | number; bottom?: string | number; left?: string | number } | string | number

interface ToastClassnames {
  toast?: string
  title?: string
  description?: string
  loader?: string
  closeButton?: string
  cancelButton?: string
  actionButton?: string
  success?: string
  error?: string
  info?: string
  warning?: string
  loading?: string
  default?: string
  content?: string
  icon?: string
}

interface ToastIcons {
  success?: ComponentChildren
  info?: ComponentChildren
  warning?: ComponentChildren
  error?: ComponentChildren
  loading?: ComponentChildren
  close?: ComponentChildren
}

interface Action {
  label: ComponentChildren
  onClick(event: TargetedMouseEvent<HTMLButtonElement>): void
}

interface ToastT {
  id: number | string
  toasterId?: string
  title?: Title
  type?: ToastTypes
  icon?: ComponentChildren
  richColors?: boolean
  invert?: boolean
  closeButton?: boolean
  dismissible?: boolean
  description?: Title
  duration?: number
  delete?: boolean
  action?: Action | ComponentChildren
  cancel?: Action | ComponentChildren
  onDismiss?(toast: ToastT): void
  onAutoClose?(toast: ToastT): void
  cancelButtonStyle?: CSSProperties
  actionButtonStyle?: CSSProperties
  style?: CSSProperties
  unstyled?: boolean
  className?: string
  classNames?: ToastClassnames
  descriptionClassName?: string
  position?: Position
  testId?: string
}

type ExternalToast = Omit<ToastT, "id" | "type" | "title" | "delete"> & { id?: number | string }

interface ToastToDismiss {
  id: number | string
  dismiss: boolean
}

interface ToastOptions {
  className?: string
  closeButton?: boolean
  descriptionClassName?: string
  style?: CSSProperties
  cancelButtonStyle?: CSSProperties
  actionButtonStyle?: CSSProperties
  duration?: number
  unstyled?: boolean
  classNames?: ToastClassnames
  closeButtonAriaLabel?: string
}

export interface ToasterProps {
  id?: string
  invert?: boolean
  theme?: "light" | "dark" | "system"
  position?: Position
  hotkey?: string[]
  richColors?: boolean
  expand?: boolean
  duration?: number
  gap?: number
  visibleToasts?: number
  closeButton?: boolean
  toastOptions?: ToastOptions
  className?: string
  style?: CSSProperties
  offset?: Offset
  mobileOffset?: Offset
  dir?: "rtl" | "ltr" | "auto"
  swipeDirections?: SwipeDirection[]
  icons?: ToastIcons
  customAriaLabel?: string
  containerAriaLabel?: string
}

interface HeightT {
  height: number
  toastId: number | string
  position?: Position
}

type SetHeights = (update: (heights: HeightT[]) => HeightT[]) => void

// state: the toasts live outside any component so toast() works anywhere

type Subscriber = (toast: ToastT | ToastToDismiss) => void

// kept for toasts that were dismissed but may be updated again by id; trimmed so a long-running app does not hold on to them
const MAX_HISTORY_SIZE = 100

let toastsCounter = 1
let history: ToastT[] = []
const subscribers: Subscriber[] = []
const dismissedToasts = new Set<number | string>()
const pendingDismissals = new Map<number | string, number>()

const getActiveToasts = () => history.filter((toast) => !dismissedToasts.has(toast.id))
const publish = (toast: ToastT | ToastToDismiss) => subscribers.forEach((subscriber) => subscriber(toast))

function subscribe(subscriber: Subscriber) {
  subscribers.push(subscriber)
  // a toast can be created before the Toaster subscribed (an effect above it, say): replay what is still active
  getActiveToasts().forEach(subscriber)
  return () => void subscribers.splice(subscribers.indexOf(subscriber), 1)
}

function trimHistory() {
  let toRemove = history.length - MAX_HISTORY_SIZE
  if (toRemove <= 0) return
  history = history.filter((toast) => {
    if (toRemove > 0 && dismissedToasts.has(toast.id)) {
      dismissedToasts.delete(toast.id)
      toRemove--
      return false
    }
    return true
  })
}

function create(data: ExternalToast & { message?: Title; type?: ToastTypes }) {
  const { message, ...rest } = data
  const id = typeof data.id === "number" || data.id ? data.id : toastsCounter++

  // a dismissal that has not reached the Toaster yet is cancelled: the toast is still on screen, so this is an update of it
  const pending = pendingDismissals.get(id)
  if (pending !== undefined) {
    cancelAnimationFrame(pending)
    pendingDismissals.delete(id)
    dismissedToasts.delete(id)
  }

  const wasDismissed = dismissedToasts.has(id)
  const dismissible = data.dismissible === undefined ? true : data.dismissible
  if (wasDismissed) {
    // the old toast is gone: start a new one instead of merging, or its props (an action, say) leak into it
    dismissedToasts.delete(id)
    history = history.filter((toast) => toast.id !== id)
  }

  const existing = wasDismissed ? undefined : history.find((toast) => toast.id === id)
  if (existing) {
    publish({ ...existing, ...data, id, title: message })
    history = history.map((toast) => (toast.id === id ? { ...toast, ...data, id, dismissible, title: message } : toast))
  } else {
    const toast = { title: message, ...rest, dismissible, id }
    publish(toast)
    history = [...history, toast]
    trimHistory()
  }
  return id
}

function dismiss(id?: number | string) {
  if (id == null) {
    getActiveToasts().forEach((toast) => {
      dismissedToasts.add(toast.id)
      publish({ id: toast.id, dismiss: true })
    })
    return id
  }
  dismissedToasts.add(id)
  const alreadyPending = pendingDismissals.get(id)
  if (alreadyPending !== undefined) cancelAnimationFrame(alreadyPending)
  pendingDismissals.set(
    id,
    requestAnimationFrame(() => {
      pendingDismissals.delete(id)
      publish({ id, dismiss: true })
    })
  )
  return id
}

// `type: undefined` resets the type when this updates a toast that had one
const typed = (type?: ToastTypes) => (message: Title, data?: ExternalToast) => create({ ...data, message, type })

export const toast = Object.assign(typed(), {
  success: typed("success"),
  info: typed("info"),
  warning: typed("warning"),
  error: typed("error"),
  loading: typed("loading"),
  dismiss,
})

// assets

const SuccessIcon = (
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" height="20" width="20" aria-hidden="true">
    <path
      fill-rule="evenodd"
      d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.857-9.809a.75.75 0 00-1.214-.882l-3.483 4.79-1.88-1.88a.75.75 0 10-1.06 1.061l2.5 2.5a.75.75 0 001.137-.089l4-5.5z"
      clip-rule="evenodd"
    />
  </svg>
)

const WarningIcon = (
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" height="20" width="20" aria-hidden="true">
    <path
      fill-rule="evenodd"
      d="M9.401 3.003c1.155-2 4.043-2 5.197 0l7.355 12.748c1.154 2-.29 4.5-2.599 4.5H4.645c-2.309 0-3.752-2.5-2.598-4.5L9.4 3.003zM12 8.25a.75.75 0 01.75.75v3.75a.75.75 0 01-1.5 0V9a.75.75 0 01.75-.75zm0 8.25a.75.75 0 100-1.5.75.75 0 000 1.5z"
      clip-rule="evenodd"
    />
  </svg>
)

const InfoIcon = (
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" height="20" width="20" aria-hidden="true">
    <path
      fill-rule="evenodd"
      d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z"
      clip-rule="evenodd"
    />
  </svg>
)

const ErrorIcon = (
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" height="20" width="20" aria-hidden="true">
    <path
      fill-rule="evenodd"
      d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z"
      clip-rule="evenodd"
    />
  </svg>
)

const CloseIcon = (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="12"
    height="12"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <line x1="18" y1="6" x2="6" y2="18"></line>
    <line x1="6" y1="6" x2="18" y2="18"></line>
  </svg>
)

const ASSETS: Partial<Record<ToastTypes, ComponentChildren>> = { success: SuccessIcon, info: InfoIcon, warning: WarningIcon, error: ErrorIcon }
const getAsset = (type?: ToastTypes) => (type && ASSETS[type]) || null

const bars = Array.from({ length: 12 }, (_, i) => i)

function Loader({ visible, className }: { visible: boolean; className?: string }) {
  return (
    <div className={cn("sonner-loading-wrapper", className)} data-visible={visible}>
      <div className="sonner-spinner">
        {bars.map((i) => (
          <div className="sonner-loading-bar" key={i} />
        ))}
      </div>
    </div>
  )
}

// Toast

const VISIBLE_TOASTS_AMOUNT = 3
const VIEWPORT_OFFSET = "24px"
const MOBILE_VIEWPORT_OFFSET = "16px"
const TOAST_LIFETIME = 4000
const TOAST_WIDTH = 356
const GAP = 14
const SWIPE_THRESHOLD = 45
// equal to the exit animation duration
const TIME_BEFORE_UNMOUNT = 200
const DEFAULT_HOTKEY = ["altKey", "KeyT"]

function cn(...classes: (string | undefined)[]) {
  return classes.filter(Boolean).join(" ")
}

// "center" never matches a swipe, so it stays in the list
const getDefaultSwipeDirections = (position: Position) => position.split("-") as SwipeDirection[]

const isAction = (action: Action | ComponentChildren): action is Action =>
  typeof action === "object" && action !== null && "label" in action

function useIsDocumentHidden() {
  const [isDocumentHidden, setIsDocumentHidden] = useState(document.hidden)
  useEffect(() => {
    const callback = () => setIsDocumentHidden(document.hidden)
    document.addEventListener("visibilitychange", callback)
    return () => document.removeEventListener("visibilitychange", callback)
  }, [])
  return isDocumentHidden
}

interface ToastProps {
  toast: ToastT
  toasts: ToastT[]
  index: number
  swipeDirections?: SwipeDirection[]
  expanded: boolean
  invert?: boolean
  heights: HeightT[]
  setHeights: SetHeights
  removeToast(toast: ToastT): void
  gap: number
  position: Position
  visibleToasts: number
  expandByDefault?: boolean
  closeButton?: boolean
  interacting: boolean
  style?: CSSProperties
  cancelButtonStyle?: CSSProperties
  actionButtonStyle?: CSSProperties
  duration?: number
  className?: string
  unstyled?: boolean
  descriptionClassName?: string
  classNames?: ToastClassnames
  icons?: ToastIcons
  closeButtonAriaLabel?: string
  defaultRichColors?: boolean
}

function Toast(props: ToastProps) {
  const {
    invert: ToasterInvert,
    toast,
    unstyled,
    interacting,
    setHeights,
    visibleToasts,
    heights,
    index,
    toasts,
    expanded,
    removeToast,
    defaultRichColors,
    closeButton: closeButtonFromToaster,
    style,
    cancelButtonStyle,
    actionButtonStyle,
    className = "",
    descriptionClassName = "",
    duration: durationFromToaster,
    position,
    gap,
    expandByDefault,
    classNames,
    icons,
    closeButtonAriaLabel = "Close toast",
  } = props
  const [swipeDirection, setSwipeDirection] = useState<"x" | "y" | null>(null)
  const [swipeOutDirection, setSwipeOutDirection] = useState<"left" | "right" | "up" | "down" | null>(null)
  const [mounted, setMounted] = useState(false)
  const [removed, setRemoved] = useState(false)
  const [swiping, setSwiping] = useState(false)
  const [swipeOut, setSwipeOut] = useState(false)
  const [isSwiped, setIsSwiped] = useState(false)
  const [offsetBeforeRemove, setOffsetBeforeRemove] = useState(0)
  const [initialHeight, setInitialHeight] = useState(0)
  const remainingTime = useRef(toast.duration || durationFromToaster || TOAST_LIFETIME)
  const dragStartTime = useRef(0)
  const toastRef = useRef<HTMLLIElement | null>(null)
  const isFront = index === 0
  const isVisible = index + 1 <= visibleToasts
  const toastType = toast.type
  // toasts created with toast() have no type: classNames.default is the key for those
  const toastTypeKey = toastType ?? "default"
  const dismissible = toast.dismissible !== false
  // the height index is used for the offset because it updates before the toast array, so the new layout is known sooner
  const heightIndex = useMemo(() => heights.findIndex((height) => height.toastId === toast.id), [heights, toast.id])
  const closeButton = toast.closeButton ?? closeButtonFromToaster
  const duration = toast.duration || durationFromToaster || TOAST_LIFETIME
  const closeTimerStartTimeRef = useRef(0)
  const offset = useRef(0)
  const lastCloseTimerStartTimeRef = useRef(0)
  const pointerStartRef = useRef<{ x: number; y: number } | null>(null)
  const [y, x] = position.split("-")
  const toastsHeightBefore = useMemo(
    () => heights.reduce((prev, curr, reducerIndex) => (reducerIndex >= heightIndex ? prev : prev + curr.height), 0),
    [heights, heightIndex]
  )
  const isDocumentHidden = useIsDocumentHidden()
  const swipeDirections = useMemo(
    () => props.swipeDirections ?? getDefaultSwipeDirections(position),
    [props.swipeDirections, position]
  )

  const invert = toast.invert || ToasterInvert
  const disabled = toastType === "loading"

  offset.current = useMemo(() => heightIndex * gap + toastsHeightBefore, [heightIndex, gap, toastsHeightBefore])

  useEffect(() => {
    remainingTime.current = duration
  }, [duration])

  useEffect(() => {
    // triggers the enter transition without a CSS animation
    setMounted(true)
  }, [])

  useEffect(() => {
    const toastNode = toastRef.current
    if (toastNode) {
      const height = toastNode.getBoundingClientRect().height
      setInitialHeight(height)
      setHeights((h) => [{ toastId: toast.id, height, position: toast.position }, ...h])
      return () => setHeights((h) => h.filter((height) => height.toastId !== toast.id))
    }
  }, [setHeights, toast.id])

  useLayoutEffect(() => {
    // keep the height up to date with the content in case it changes
    const toastNode = toastRef.current
    if (!mounted || !toastNode) return
    const originalHeight = toastNode.style.height
    toastNode.style.height = "auto"
    const newHeight = toastNode.getBoundingClientRect().height
    toastNode.style.height = originalHeight

    setInitialHeight(newHeight)
    setHeights((heights) => {
      if (!heights.find((height) => height.toastId === toast.id))
        return [{ toastId: toast.id, height: newHeight, position: toast.position }, ...heights]
      return heights.map((height) => (height.toastId === toast.id ? { ...height, height: newHeight } : height))
    })
  }, [mounted, toast.title, toast.description, setHeights, toast.id, toast.action, toast.cancel])

  const deleteToast = useCallback(() => {
    // the offset is saved for the exit animation
    setRemoved(true)
    setOffsetBeforeRemove(offset.current)
    setHeights((h) => h.filter((height) => height.toastId !== toast.id))
    setTimeout(() => removeToast(toast), TIME_BEFORE_UNMOUNT)
  }, [toast, removeToast, setHeights])

  useEffect(() => {
    if (toast.duration === Infinity || toastType === "loading") return
    let timeoutId: ReturnType<typeof setTimeout>

    const pauseTimer = () => {
      if (lastCloseTimerStartTimeRef.current < closeTimerStartTimeRef.current) {
        remainingTime.current -= Date.now() - closeTimerStartTimeRef.current
      }
      lastCloseTimerStartTimeRef.current = Date.now()
    }

    const startTimer = () => {
      // setTimeout(fn, Infinity) fires at once, which would close the toast before anyone saw it
      if (remainingTime.current === Infinity) return
      closeTimerStartTimeRef.current = Date.now()
      timeoutId = setTimeout(() => {
        toast.onAutoClose?.(toast)
        deleteToast()
      }, remainingTime.current)
    }

    if (expanded || interacting || isDocumentHidden) pauseTimer()
    else startTimer()

    return () => clearTimeout(timeoutId)
  }, [expanded, interacting, toast, toastType, isDocumentHidden, deleteToast])

  useEffect(() => {
    if (toast.delete) {
      deleteToast()
      toast.onDismiss?.(toast)
    }
  }, [deleteToast, toast.delete])

  function getLoadingIcon() {
    if (icons?.loading) {
      return (
        <div className={cn(classNames?.loader, toast.classNames?.loader, "sonner-loader")} data-visible={toastType === "loading"}>
          {icons.loading}
        </div>
      )
    }
    return <Loader className={cn(classNames?.loader, toast.classNames?.loader)} visible={toastType === "loading"} />
  }

  const typeIcon = toastType ? icons?.[toastType] : undefined
  const icon = toast.icon || typeIcon || getAsset(toastType)
  const title = typeof toast.title === "function" ? toast.title() : toast.title
  const description = typeof toast.description === "function" ? toast.description() : toast.description

  return (
    <li
      tabIndex={0}
      ref={toastRef}
      className={cn(
        className,
        toast.className,
        classNames?.toast,
        toast.classNames?.toast,
        classNames?.[toastTypeKey],
        toast.classNames?.[toastTypeKey]
      )}
      data-sonner-toast=""
      data-rich-colors={toast.richColors ?? defaultRichColors}
      data-styled={!(toast.unstyled || unstyled)}
      data-mounted={mounted}
      data-promise={false} // sonner's attribute for promise toasts, kept so the markup matches
      data-swiped={isSwiped}
      data-removed={removed}
      data-visible={isVisible}
      data-y-position={y}
      data-x-position={x}
      data-index={index}
      data-front={isFront}
      data-swiping={swiping}
      data-dismissible={dismissible}
      data-type={toastType}
      data-invert={invert}
      data-swipe-out={swipeOut}
      data-swipe-direction={swipeOutDirection}
      data-expanded={Boolean(expanded || (expandByDefault && mounted))}
      data-testid={toast.testId}
      style={
        {
          "--index": index,
          "--toasts-before": index,
          "--z-index": toasts.length - index,
          "--offset": `${removed ? offsetBeforeRemove : offset.current}px`,
          "--initial-height": expandByDefault ? "auto" : `${initialHeight}px`,
          ...style,
          ...toast.style,
        } as CSSProperties
      }
      onDragEnd={() => {
        setSwiping(false)
        setSwipeDirection(null)
        pointerStartRef.current = null
      }}
      onPointerDown={(event) => {
        if (event.button === 2) return // right click
        if (disabled || !dismissible) return
        dragStartTime.current = Date.now()
        setOffsetBeforeRemove(offset.current)
        // keeps the pointer events coming when the pointer leaves the toast while swiping
        ;(event.target as HTMLElement).setPointerCapture(event.pointerId)
        if ((event.target as HTMLElement).tagName === "BUTTON") return
        setSwiping(true)
        pointerStartRef.current = { x: event.clientX, y: event.clientY }
      }}
      onPointerUp={() => {
        if (swipeOut || !dismissible) return

        pointerStartRef.current = null
        const swipeAmountX = Number(toastRef.current?.style.getPropertyValue("--swipe-amount-x").replace("px", "") || 0)
        const swipeAmountY = Number(toastRef.current?.style.getPropertyValue("--swipe-amount-y").replace("px", "") || 0)
        const timeTaken = Date.now() - dragStartTime.current

        const swipeAmount = swipeDirection === "x" ? swipeAmountX : swipeAmountY
        const velocity = Math.abs(swipeAmount) / timeTaken

        // movement towards a direction that is not allowed is dampened, not blocked, so a fast flick can still pass the velocity check
        const isAllowedDirection =
          swipeDirection === "x"
            ? swipeDirections.includes(swipeAmountX > 0 ? "right" : "left")
            : swipeDirections.includes(swipeAmountY > 0 ? "bottom" : "top")

        if (isAllowedDirection && (Math.abs(swipeAmount) >= SWIPE_THRESHOLD || velocity > 0.11)) {
          setOffsetBeforeRemove(offset.current)
          toast.onDismiss?.(toast)
          if (swipeDirection === "x") setSwipeOutDirection(swipeAmountX > 0 ? "right" : "left")
          else setSwipeOutDirection(swipeAmountY > 0 ? "down" : "up")
          deleteToast()
          setSwipeOut(true)
          return
        }
        toastRef.current?.style.setProperty("--swipe-amount-x", "0px")
        toastRef.current?.style.setProperty("--swipe-amount-y", "0px")
        setIsSwiped(false)
        setSwiping(false)
        setSwipeDirection(null)
      }}
      onPointerMove={(event) => {
        if (!pointerStartRef.current || !dismissible) return
        if ((window.getSelection()?.toString().length ?? 0) > 0) return

        const yDelta = event.clientY - pointerStartRef.current.y
        const xDelta = event.clientX - pointerStartRef.current.x

        // the axis is locked by the first movement
        if (!swipeDirection && (Math.abs(xDelta) > 1 || Math.abs(yDelta) > 1)) {
          setSwipeDirection(Math.abs(xDelta) > Math.abs(yDelta) ? "x" : "y")
        }

        const swipeAmount = { x: 0, y: 0 }
        const getDampening = (delta: number) => 1 / (1.5 + Math.abs(delta) / 20)

        if (swipeDirection === "y") {
          if (swipeDirections.includes("top") || swipeDirections.includes("bottom")) {
            if ((swipeDirections.includes("top") && yDelta < 0) || (swipeDirections.includes("bottom") && yDelta > 0)) {
              swipeAmount.y = yDelta
            } else {
              // no jump when the movement turns into a dampened one
              const dampenedDelta = yDelta * getDampening(yDelta)
              swipeAmount.y = Math.abs(dampenedDelta) < Math.abs(yDelta) ? dampenedDelta : yDelta
            }
          }
        } else if (swipeDirection === "x") {
          if (swipeDirections.includes("left") || swipeDirections.includes("right")) {
            if ((swipeDirections.includes("left") && xDelta < 0) || (swipeDirections.includes("right") && xDelta > 0)) {
              swipeAmount.x = xDelta
            } else {
              const dampenedDelta = xDelta * getDampening(xDelta)
              swipeAmount.x = Math.abs(dampenedDelta) < Math.abs(xDelta) ? dampenedDelta : xDelta
            }
          }
        }

        if (Math.abs(swipeAmount.x) > 0 || Math.abs(swipeAmount.y) > 0) setIsSwiped(true)

        toastRef.current?.style.setProperty("--swipe-amount-x", `${swipeAmount.x}px`)
        toastRef.current?.style.setProperty("--swipe-amount-y", `${swipeAmount.y}px`)
      }}
    >
      {closeButton && toastType !== "loading" ? (
        <button
          aria-label={closeButtonAriaLabel}
          data-disabled={disabled}
          data-close-button
          onClick={
            disabled || !dismissible
              ? () => {}
              : () => {
                  deleteToast()
                  toast.onDismiss?.(toast)
                }
          }
          className={cn(classNames?.closeButton, toast.classNames?.closeButton)}
        >
          {icons?.close ?? CloseIcon}
        </button>
      ) : null}
      {(toastType || toast.icon) && toast.icon !== null && (typeIcon !== null || toast.icon) ? (
        <div data-icon="" className={cn(classNames?.icon, toast.classNames?.icon)}>
          {toastType === "loading" ? toast.icon || getLoadingIcon() : icon}
        </div>
      ) : null}

      <div data-content="" className={cn(classNames?.content, toast.classNames?.content)}>
        <div data-title="" className={cn(classNames?.title, toast.classNames?.title)}>
          {title}
        </div>
        {description ? (
          <div
            data-description=""
            className={cn(descriptionClassName, toast.descriptionClassName, classNames?.description, toast.classNames?.description)}
          >
            {description}
          </div>
        ) : null}
      </div>
      {isValidElement(toast.cancel) ? (
        toast.cancel
      ) : isAction(toast.cancel) ? (
        <button
          data-button
          data-cancel
          style={toast.cancelButtonStyle || cancelButtonStyle}
          onClick={(event) => {
            if (!isAction(toast.cancel) || !dismissible) return
            toast.cancel.onClick?.(event)
            deleteToast()
          }}
          className={cn(classNames?.cancelButton, toast.classNames?.cancelButton)}
        >
          {toast.cancel.label}
        </button>
      ) : null}
      {isValidElement(toast.action) ? (
        toast.action
      ) : isAction(toast.action) ? (
        <button
          data-button
          data-action
          style={toast.actionButtonStyle || actionButtonStyle}
          onClick={(event) => {
            if (!isAction(toast.action)) return
            toast.action.onClick?.(event)
            if (event.defaultPrevented) return
            deleteToast()
          }}
          className={cn(classNames?.actionButton, toast.classNames?.actionButton)}
        >
          {toast.action.label}
        </button>
      ) : null}
    </li>
  )
}

// Toaster

function getDocumentDirection(): ToasterProps["dir"] {
  if (typeof window === "undefined" || typeof document === "undefined") return "ltr"
  const dirAttribute = document.documentElement.getAttribute("dir")
  if (dirAttribute === "auto" || !dirAttribute) {
    return window.getComputedStyle(document.documentElement).direction as ToasterProps["dir"]
  }
  return dirAttribute as ToasterProps["dir"]
}

const px = (value: string | number) => (typeof value === "number" ? `${value}px` : value)

function assignOffset(defaultOffset: Offset | undefined, mobileOffset: Offset | undefined) {
  const styles: Record<string, string> = {}
  ;[defaultOffset, mobileOffset].forEach((offset, index) => {
    const prefix = index ? "--mobile-offset" : "--offset"
    const fallback = index ? MOBILE_VIEWPORT_OFFSET : VIEWPORT_OFFSET
    for (const key of ["top", "right", "bottom", "left"] as const) {
      const value = typeof offset === "object" ? offset?.[key] : offset
      styles[`${prefix}-${key}`] = value == null ? fallback : px(value)
    }
  })
  return styles
}

const getSystemTheme = () =>
  typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light"

export function Toaster(props: ToasterProps) {
  const {
    id,
    invert,
    position = "bottom-right",
    hotkey = DEFAULT_HOTKEY,
    expand,
    closeButton,
    className,
    offset,
    mobileOffset,
    theme = "light",
    richColors,
    duration,
    style,
    visibleToasts = VISIBLE_TOASTS_AMOUNT,
    toastOptions,
    dir = getDocumentDirection(),
    gap = GAP,
    icons,
    customAriaLabel,
    containerAriaLabel = "Notifications",
  } = props
  const [toasts, setToasts] = useState<ToastT[]>([])
  const filteredToasts = useMemo(
    () => toasts.filter((toast) => (id ? toast.toasterId === id : !toast.toasterId)),
    [toasts, id]
  )
  const possiblePositions = useMemo(
    () => Array.from(new Set([position, ...filteredToasts.flatMap((toast) => (toast.position ? [toast.position] : []))])),
    [filteredToasts, position]
  )
  const [heights, setHeights] = useState<HeightT[]>([])
  const [expanded, setExpanded] = useState(false)
  const [interacting, setInteracting] = useState(false)
  const [actualTheme, setActualTheme] = useState(theme === "system" ? getSystemTheme() : theme)

  const listRef = useRef<HTMLOListElement | null>(null)
  const hotkeyLabel = hotkey.join("+").replace(/Key/g, "").replace(/Digit/g, "")
  const lastFocusedElementRef = useRef<HTMLElement | null>(null)
  const isFocusWithinRef = useRef(false)

  const removeToast = useCallback((toastToRemove: ToastT) => {
    setToasts((toasts) => {
      if (!toasts.find((toast) => toast.id === toastToRemove.id)?.delete) dismiss(toastToRemove.id)
      return toasts.filter(({ id }) => id !== toastToRemove.id)
    })
  }, [])

  useEffect(
    () =>
      subscribe((toast) => {
        if ("dismiss" in toast) {
          // a frame apart from other updates, so they do not batch
          requestAnimationFrame(() => setToasts((toasts) => toasts.map((t) => (t.id === toast.id ? { ...t, delete: true } : t))))
          return
        }
        // one timer per toast keeps toasts created in the same tick from batching into one render, which would skip the enter transition
        setTimeout(() =>
          setToasts((toasts) => {
            const index = toasts.findIndex((t) => t.id === toast.id)
            // update the toast if it already exists
            if (index !== -1) return [...toasts.slice(0, index), { ...toasts[index]!, ...toast }, ...toasts.slice(index + 1)]
            return [toast, ...toasts]
          })
        )
      }),
    []
  )

  useEffect(() => {
    if (theme !== "system") {
      setActualTheme(theme)
      return
    }
    const query = window.matchMedia?.("(prefers-color-scheme: dark)")
    const update = () => setActualTheme(query?.matches ? "dark" : "light")
    update()
    query?.addEventListener("change", update)
    return () => query?.removeEventListener("change", update)
  }, [theme])

  useEffect(() => {
    // expanded is always false when no toast, or only one, is left
    if (toasts.length <= 1) setExpanded(false)
  }, [toasts])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const isHotkeyPressed = hotkey.length > 0 && hotkey.every((key) => (event as unknown as Record<string, unknown>)[key] || event.code === key)
      if (isHotkeyPressed) {
        setExpanded(true)
        listRef.current?.focus()
      }
      if (event.code === "Escape" && listRef.current?.contains(document.activeElement)) setExpanded(false)
    }
    document.addEventListener("keydown", handleKeyDown)
    return () => document.removeEventListener("keydown", handleKeyDown)
  }, [hotkey])

  useEffect(() => {
    if (listRef.current) {
      return () => {
        if (lastFocusedElementRef.current) {
          lastFocusedElementRef.current.focus({ preventScroll: true })
          lastFocusedElementRef.current = null
          isFocusWithinRef.current = false
        }
      }
    }
  }, [listRef.current])

  return (
    // out of the normal navigation flow, reachable through the hotkey
    <section
      aria-label={customAriaLabel ?? `${containerAriaLabel} ${hotkeyLabel}`}
      tabIndex={-1}
      aria-live="polite"
      aria-relevant="additions text"
      aria-atomic="false"
      data-react-aria-top-layer // react-aria's marker, kept so the markup matches sonner's
    >
      {filteredToasts.length
        ? possiblePositions.map((position, index) => {
            const [y, x] = position.split("-")
            return (
              <ol
                key={position}
                dir={dir === "auto" ? getDocumentDirection() : dir}
                tabIndex={-1}
                ref={listRef}
                className={className}
                data-sonner-toaster
                data-sonner-theme={actualTheme}
                data-y-position={y}
                data-x-position={x}
                style={
                  {
                    "--front-toast-height": `${heights[0]?.height || 0}px`,
                    "--width": `${TOAST_WIDTH}px`,
                    "--gap": `${gap}px`,
                    ...style,
                    ...assignOffset(offset, mobileOffset),
                  } as CSSProperties
                }
                // React's onFocus and onBlur bubble, Preact's are the native ones that do not
                onFocusOut={(event) => {
                  if (isFocusWithinRef.current && !event.currentTarget.contains(event.relatedTarget as Node | null)) {
                    isFocusWithinRef.current = false
                    if (lastFocusedElementRef.current) {
                      lastFocusedElementRef.current.focus({ preventScroll: true })
                      lastFocusedElementRef.current = null
                    }
                  }
                }}
                onFocusIn={(event) => {
                  if (event.target instanceof HTMLElement && event.target.dataset.dismissible === "false") return
                  if (!isFocusWithinRef.current) {
                    isFocusWithinRef.current = true
                    lastFocusedElementRef.current = event.relatedTarget as HTMLElement | null
                  }
                }}
                onMouseEnter={() => setExpanded(true)}
                onMouseMove={() => setExpanded(true)}
                onMouseLeave={() => {
                  // not while interacting with a toast, a swipe for one
                  if (!interacting) setExpanded(false)
                }}
                onDragEnd={() => setExpanded(false)}
                onPointerDown={(event) => {
                  if (event.target instanceof HTMLElement && event.target.dataset.dismissible === "false") return
                  setInteracting(true)
                }}
                onPointerUp={() => setInteracting(false)}
              >
                {filteredToasts
                  .filter((toast) => (!toast.position && index === 0) || toast.position === position)
                  .map((toast, index) => (
                    <Toast
                      key={toast.id}
                      icons={icons}
                      index={index}
                      toast={toast}
                      defaultRichColors={richColors}
                      duration={toastOptions?.duration ?? duration}
                      className={toastOptions?.className}
                      descriptionClassName={toastOptions?.descriptionClassName}
                      invert={invert}
                      visibleToasts={visibleToasts}
                      closeButton={toastOptions?.closeButton ?? closeButton}
                      interacting={interacting}
                      position={position}
                      style={toastOptions?.style}
                      unstyled={toastOptions?.unstyled}
                      classNames={toastOptions?.classNames}
                      cancelButtonStyle={toastOptions?.cancelButtonStyle}
                      actionButtonStyle={toastOptions?.actionButtonStyle}
                      closeButtonAriaLabel={toastOptions?.closeButtonAriaLabel}
                      removeToast={removeToast}
                      toasts={filteredToasts.filter((t) => t.position == toast.position)}
                      heights={heights.filter((h) => h.position == toast.position)}
                      setHeights={setHeights}
                      expandByDefault={expand}
                      gap={gap}
                      expanded={expanded}
                      swipeDirections={props.swipeDirections}
                    />
                  ))}
              </ol>
            )
          })
        : null}
    </section>
  )
}
