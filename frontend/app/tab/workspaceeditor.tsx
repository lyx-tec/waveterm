import { fireAndForget, makeIconClass } from "@/util/util";
import clsx from "clsx";
import { memo, useEffect, useRef, useState } from "react";
import { Button } from "../element/button";
import { Input } from "../element/input";
import { WorkspaceService } from "../store/services";
import { WorkspaceConnectionSelector } from "./workspaceconnectionselector";
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
            <WorkspaceConnectionSelector
                connectionNames={connectionNames}
                value={connName}
                onChange={onConnNameChange}
            />
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
