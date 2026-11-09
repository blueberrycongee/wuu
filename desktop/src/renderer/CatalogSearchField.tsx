import { Search } from "./WuuIcons";

export function CatalogSearchField({
  value,
  placeholder,
  onValueChange,
}: {
  value: string;
  placeholder: string;
  onValueChange: (value: string) => void;
}): JSX.Element {
  return (
    <label className="catalog-search">
      <Search aria-hidden="true" />
      <input
        type="search"
        value={value}
        aria-label={placeholder}
        placeholder={placeholder}
        onChange={(event) => onValueChange(event.currentTarget.value)}
      />
    </label>
  );
}
