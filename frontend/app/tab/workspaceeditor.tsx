import { fireAndForget, makeIconClass } from "@/util/util";
import clsx from "clsx";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "../element/button";
import { Input } from "../element/input";
import { WorkspaceService } from "../store/services";
import "./workspaceeditor.scss";

interface ColorSelectorProps {
    colors: string[];
    selectedColor?: string;
    onSelect: (color: string) => void;
    className?: string;
}

const ColorSelector = memo(({ colors, selectedColor, onSelect, className }: ColorSelectorProps) => {
    const handleColorClick = (color: string) => {
        onSelect(color);
    };

    return (
        <div className={clsx("color-selector", className)}>
            {colors.map((color) => (
                <div
                    key={color}
                    className={clsx("color-circle", { selected: selectedColor === color })}
                    style={{ backgroundColor: color }}
                    onClick={() => handleColorClick(color)}
                />
            ))}
        </div>
    );
});

interface IconSelectorProps {
    icons: string[];
    selectedIcon?: string;
    onSelect: (icon: string) => void;
    className?: string;
}

const IconSelector = memo(({ icons, selectedIcon, onSelect, className }: IconSelectorProps) => {
    const handleIconClick = (icon: string) => {
        onSelect(icon);
    };

    return (
        <div className={clsx("icon-selector", className)}>
            {icons.map((icon) => {
                const iconClass = makeIconClass(icon, true);
                return (
                    <i
                        key={icon}
                        className={clsx(iconClass, "icon-item", { selected: selectedIcon === icon })}
                        onClick={() => handleIconClick(icon)}
                    />
                );
            })}
        </div>
    );
});

interface ConnectionSelectorProps {
    connectionNames: string[];
    value: string;
    onChange: (value: string) => void;
}

const ConnectionSelector = memo(({ connectionNames, value, onChange }: ConnectionSelectorProps) => {
    const [open, setOpen] = useState(false);
    const ref = useRef<HTMLDivElement>(null);

    useEffect(() => {
        const handleClickOutside = (e: MouseEvent) => {
            if (ref.current && !ref.current.contains(e.target as Node)) {
                setOpen(false);
            }
        };
        document.addEventListener("mousedown", handleClickOutside);
        return () => document.removeEventListener("mousedown", handleClickOutside);
    }, []);

    const items = useMemo(() => {
        const sorted = [...connectionNames].sort();
        return [{ label: "(none)", value: "" }, { divider: true as const }, ...sorted.map((n) => ({ label: n, value: n }))];
    }, [connectionNames]);

    const handleSelect = (val: string) => {
        onChange(val);
        setOpen(false);
    };

    return (
        <div className="connection-selector" ref={ref}>
            <div className="connection-input-wrapper">
                <Input
                    className="connection-input py-[3px]"
                    value={value}
                    onChange={onChange}
                    placeholder="Default connection"
                    onFocus={() => setOpen(true)}
                />
                <i
                    className={clsx("fa-sharp fa-solid fa-chevron-down dropdown-arrow", { open })}
                    onClick={() => setOpen(!open)}
                />
            </div>
            {open && (
                <div className="connection-dropdown">
                    {items.map((item, idx) => {
                        if ("divider" in item) {
                            return <div key={idx} className="dropdown-divider" />;
                        }
                        return (
                            <div
                                key={item.value}
                                className={clsx("dropdown-item", { selected: item.value === value })}
                                onClick={() => handleSelect(item.value)}
                            >
                                {item.label}
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
});

interface WorkspaceEditorProps {
    title: string;
    icon: string;
    color: string;
    connName: string;
    cwd: string;
    focusInput: boolean;
    onTitleChange: (newTitle: string) => void;
    onColorChange: (newColor: string) => void;
    onIconChange: (newIcon: string) => void;
    onConnNameChange: (newConnName: string) => void;
    onCwdChange: (newCwd: string) => void;
    onDeleteWorkspace: () => void;
}

const WorkspaceEditorComponent = ({
    title,
    icon,
    color,
    connName,
    cwd,
    focusInput,
    onTitleChange,
    onColorChange,
    onIconChange,
    onConnNameChange,
    onCwdChange,
    onDeleteWorkspace,
}: WorkspaceEditorProps) => {
    const inputRef = useRef<HTMLInputElement>(null);

    const [colors, setColors] = useState<string[]>([]);
    const [icons, setIcons] = useState<string[]>([]);
    const [connectionNames, setConnectionNames] = useState<string[]>([]);

    useEffect(() => {
        fireAndForget(async () => {
            const [clrs, ics, conns] = await Promise.all([
                WorkspaceService.GetColors(),
                WorkspaceService.GetIcons(),
                WorkspaceService.GetConnectionNames(),
            ]);
            setColors(clrs);
            setIcons(ics);
            setConnectionNames(conns);
        });
    }, []);

    useEffect(() => {
        if (focusInput && inputRef.current) {
            inputRef.current.focus();
            inputRef.current.select();
        }
    }, [focusInput]);

    return (
        <div className="workspace-editor">
            <Input
                ref={inputRef}
                className={clsx("py-[3px]", { error: title === "" })}
                onChange={onTitleChange}
                value={title}
                autoFocus
                autoSelect
            />
            <ConnectionSelector connectionNames={connectionNames} value={connName} onChange={onConnNameChange} />
            <Input className="py-[3px]" onChange={onCwdChange} value={cwd} placeholder="Default work directory" />
            <ColorSelector selectedColor={color} colors={colors} onSelect={onColorChange} />
            <IconSelector selectedIcon={icon} icons={icons} onSelect={onIconChange} />
            <div className="delete-ws-btn-wrapper">
                <Button className="ghost red text-[12px] bold" onClick={onDeleteWorkspace}>
                    Delete workspace
                </Button>
            </div>
        </div>
    );
};

export const WorkspaceEditor = memo(WorkspaceEditorComponent);
