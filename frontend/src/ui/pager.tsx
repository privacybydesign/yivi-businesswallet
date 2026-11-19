import { useTranslation } from "react-i18next";
import { Button } from "./button";

export function Pager({
  page,
  pages,
  from,
  to,
  total,
  onPage,
}: Readonly<{
  page: number;
  pages: number;
  from: number;
  to: number;
  total: string;
  onPage: (page: number) => void;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  if (pages <= 1) {
    return null;
  }
  return (
    <nav
      aria-label={t("common.pages")}
      className="border-line flex items-center justify-between gap-3 border-t px-5 py-3"
    >
      <span className="text-ink-soft text-[13px]">
        {t("common.pageRange", { from, to, total })}
      </span>
      <div className="flex gap-2">
        <Button
          variant="secondary"
          size="sm"
          icon="chevron_left"
          disabled={page === 0}
          onClick={() => onPage(page - 1)}
        >
          {t("common.previousPage")}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          icon="chevron_right"
          disabled={page >= pages - 1}
          onClick={() => onPage(page + 1)}
        >
          {t("common.nextPage")}
        </Button>
      </div>
    </nav>
  );
}
