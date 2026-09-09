import { cva, type VariantProps } from 'class-variance-authority'
export { default as Button } from './Button.vue'
export const buttonVariants = cva(
  // Tailwind v4 的按钮默认鼠标样式为 default。这里显式保留可点击提示，
  // 同时让禁用态保持不可操作，避免工作台各类主按钮看起来像静态标签。
  'inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-md border border-transparent text-sm font-medium transition-colors outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
  { variants: {
    variant: {
      default: 'bg-primary text-primary-foreground hover:bg-primary/90',
      destructive: 'bg-destructive text-white hover:bg-destructive/90 focus-visible:ring-destructive/30',
      outline: 'border-border bg-background text-foreground hover:bg-accent hover:text-accent-foreground',
      secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/80',
      ghost: 'bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground',
      link: 'bg-transparent text-primary underline-offset-4 hover:underline',
    },
    // 工具栏与原有表单控件对齐；固定控件高度仍保留用户的字号缩放和移动点击区域。
    size: { default: 'h-9 px-4 py-2', sm: 'h-8 gap-1.5 px-3 text-xs', toolbar: 'h-[32px] gap-1.5 px-[9px] py-[5px] text-[12px] rounded-[6px] shadow-none max-[820px]:h-[40px] max-[820px]:text-[13px]', lg: 'h-10 px-6', icon: 'size-9 p-0', 'icon-sm': 'size-8 p-0', 'icon-lg': 'size-10 p-0' },
  }, defaultVariants: { variant: 'default', size: 'default' } },
)
export type ButtonVariants = VariantProps<typeof buttonVariants>
