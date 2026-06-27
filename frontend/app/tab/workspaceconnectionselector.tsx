// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import clsx from "clsx";
import { OverlayScrollbarsComponent } from "overlayscrollbars-react";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Input } from "../element/input";

interface ConnectionSelectorProps {
    connectionNames: string[];
    value: string;
    onChange: (value: string) => void;
}

const WorkspaceConnectionSelectorComponent = ({ connectionNames, value, onChange }: ConnectionSelectorProps) => {
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
        return [
            { label: "(none)", value: "" },
            { divider: true as const },
            ...sorted.map((n) => ({ label: n, value: n })),
        ];
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
                <OverlayScrollbarsComponent
                    className="connection-dropdown"
                    options={{ scrollbars: { autoHide: "leave" } }}
                >
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
                </OverlayScrollbarsComponent>
            )}
        </div>
    );
};

export const WorkspaceConnectionSelector = memo(WorkspaceConnectionSelectorComponent);
